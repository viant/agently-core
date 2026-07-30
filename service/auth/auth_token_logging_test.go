package auth

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/viant/agently-core/internal/authlog"
	"golang.org/x/oauth2"
)

func captureStandardLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	log.SetOutput(buffer)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
	return buffer
}

func TestAuthTokenLogging_AlwaysOnAndRedactsCanarySecrets(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "")
	buffer := captureStandardLog(t)
	canaries := []string{
		"CANARY_BEARER",
		"CANARY_BASIC",
		"CANARY_CLIENT_SECRET",
		"CANARY_REFRESH",
		"CANARY_COOKIE",
		"CANARY_QUERY",
		"CANARY_PASSWORD",
	}
	rawErr := errors.New(
		`Authorization: Bearer CANARY_BEARER Basic CANARY_BASIC ` +
			`client_secret=CANARY_CLIENT_SECRET ` +
			`{"refresh_token":"CANARY_REFRESH","cookie":"CANARY_COOKIE"} ` +
			`https://user:CANARY_PASSWORD@idp.example.test/token?access_token=CANARY_QUERY`,
	)
	authlog.Log(context.Background(), authlog.Event{
		Op:             "refresh",
		UserID:         "canonical-user-id",
		Provider:       "oauth",
		Endpoint:       "https://endpoint-user:password@tokens.example.test/oauth/token?secret=query",
		HTTPStatus:     401,
		Classification: "client_auth_rejected",
		Action:         "preserve",
		Elapsed:        25 * time.Millisecond,
		Err:            rawErr,
	})

	output := buffer.String()
	if !strings.Contains(output, "[auth-token]") {
		t.Fatalf("always-on token log missing with AGENTLY_DEBUG unset: %q", output)
	}
	if !strings.Contains(output, `endpoint_host="tokens.example.test"`) ||
		!strings.Contains(output, `endpoint_path="/oauth/token"`) {
		t.Fatalf("safe endpoint fields missing: %q", output)
	}
	for _, canary := range canaries {
		if strings.Contains(output, canary) {
			t.Fatalf("log leaked %q: %q", canary, output)
		}
	}
	if strings.Contains(output, "endpoint-user") || strings.Contains(output, "?secret=") {
		t.Fatalf("log leaked endpoint userinfo/query: %q", output)
	}
}

func TestAuthTokenLogging_SanitizesAuthStoreErrorsAndTruncatesUnicode(t *testing.T) {
	buffer := captureStandardLog(t)
	logDatlyStoreOp(context.Background(), "session", "get", "session-id", time.Now(),
		errors.New(`Bearer CANARY_STORE_BEARER enc_token=CANARY_ENCRYPTED dsn="CANARY_DSN"`))
	output := buffer.String()
	if !strings.Contains(output, "[auth-store]") {
		t.Fatalf("auth-store error log missing: %q", output)
	}
	for _, canary := range []string{"CANARY_STORE_BEARER", "CANARY_ENCRYPTED", "CANARY_DSN"} {
		if strings.Contains(output, canary) {
			t.Fatalf("auth-store log leaked %q: %q", canary, output)
		}
	}

	sanitized := authlog.Sanitize(strings.Repeat("界", 600))
	if got := utf8.RuneCountInString(sanitized); got != 512 {
		t.Fatalf("sanitized rune count = %d, want 512", got)
	}
}

func TestAuthTokenLogging_StoreScanDecryptLeaseAndPersistErrorsAreNotSilent(t *testing.T) {
	t.Setenv("AGENTLY_DEBUG", "")
	buffer := captureStandardLog(t)
	for _, op := range []string{"scan", "scan_row", "decrypt", "lease", "release", "put", "cas_put"} {
		logDatlyStoreOp(context.Background(), "token", op, "canonical-user|oauth", time.Now(),
			errors.New("operation failed client_secret=CANARY_"+strings.ToUpper(op)))
	}
	output := buffer.String()
	for _, op := range []string{"scan", "scan_row", "decrypt", "lease", "release", "put", "cas_put"} {
		if !strings.Contains(output, `op="store_`+op+`"`) {
			t.Fatalf("missing always-on %s error log: %q", op, output)
		}
		if strings.Contains(output, "CANARY_"+strings.ToUpper(op)) {
			t.Fatalf("%s log leaked canary: %q", op, output)
		}
	}
}

func TestAuthTokenLogging_RefreshResponseBodyIsNeverLogged(t *testing.T) {
	buffer := captureStandardLog(t)
	ctx := oauthRefreshContext(func(req *http.Request) (*http.Response, error) {
		return oauthRefreshResponse(req, http.StatusBadRequest, "application/json",
			`{"error":"invalid_request","error_description":"CANARY_RESPONSE_BODY"}`), nil
	})
	_, err := refreshOAuthToken(ctx,
		oauthRefreshConfig("https://tokens.example.test/no-body-log", oauth2.AuthStyleInHeader),
		&oauth2.Token{RefreshToken: "CANARY_REQUEST_TOKEN"},
		nil,
		"",
	)
	if err == nil {
		t.Fatal("refreshOAuthToken() error = nil")
	}
	output := buffer.String()
	for _, canary := range []string{"CANARY_RESPONSE_BODY", "CANARY_REQUEST_TOKEN"} {
		if strings.Contains(output, canary) {
			t.Fatalf("refresh log leaked %q: %q", canary, output)
		}
	}
}
