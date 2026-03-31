package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/viant/scy/kms"
	"github.com/viant/scy/kms/blowfish"
)

type tokenRow struct {
	UserID       string
	Provider     string
	EncToken     string
	CreatedAt    string
	UpdatedAt    string
	Version      int
	RefreshState string
}

type encToken struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

type jwtSummary struct {
	Present bool
	FP      string
	Sub     string
	Email   string
	Iss     string
	Aud     string
	Azp     string
	Exp     string
	Iat     string
}

var cipher = blowfish.Cipher{}

func main() {
	var (
		dbPath         = flag.String("db", "", "path to agently-core.db")
		userID         = flag.String("user", "awitas_viant_devtest", "user_id to inspect")
		provider       = flag.String("provider", "oauth", "provider to inspect")
		salt           = flag.String("salt", "idp_viant.enc|blowfish://default", "token encryption salt/configURL")
		callURL        = flag.String("call-url", "", "optional JSON-RPC endpoint to call")
		callMethod     = flag.String("call-method", "message/send", "JSON-RPC method")
		callParams     = flag.String("call-params", "", "JSON string for JSON-RPC params")
		tokenKind      = flag.String("token-kind", "id", "token kind to use for call: id|access")
		sessionBaseURL = flag.String("session-base-url", "", "optional app base URL for session bootstrap before shared A2A call")
		a2aAgentID     = flag.String("a2a-agent-id", "", "agent id for shared A2A call when using session-base-url")
	)
	flag.Parse()

	if strings.TrimSpace(*dbPath) == "" {
		fmt.Fprintln(os.Stderr, "--db is required")
		os.Exit(2)
	}

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		fail(err)
	}
	defer db.Close()

	ctx := context.Background()
	row, err := loadRow(ctx, db, strings.TrimSpace(*userID), strings.TrimSpace(*provider))
	if err != nil {
		fail(err)
	}
	if row == nil {
		fmt.Printf("No token row found for user=%q provider=%q\n", *userID, *provider)
		return
	}

	token, err := decryptToken(ctx, strings.TrimSpace(*salt), row.EncToken)
	if err != nil {
		fail(fmt.Errorf("decrypt token: %w", err))
	}

	fmt.Printf("DB: %s\n", *dbPath)
	fmt.Printf("User: %s\n", row.UserID)
	fmt.Printf("Provider: %s\n", row.Provider)
	fmt.Printf("CreatedAt: %s\n", row.CreatedAt)
	fmt.Printf("UpdatedAt: %s\n", row.UpdatedAt)
	fmt.Printf("Version: %d\n", row.Version)
	fmt.Printf("RefreshStatus: %s\n", row.RefreshState)
	fmt.Println()
	printSummary("ID Token", summarizeJWT(token.IDToken))
	fmt.Println()
	printSummary("Access Token", summarizeJWT(token.AccessToken))
	fmt.Println()
	fmt.Printf("Refresh Token Present: %v\n", strings.TrimSpace(token.RefreshToken) != "")
	if !token.ExpiresAt.IsZero() {
		fmt.Printf("Stored ExpiresAt: %s\n", token.ExpiresAt.Format(time.RFC3339))
	}

	if strings.TrimSpace(*callURL) != "" {
		var bearer string
		switch strings.ToLower(strings.TrimSpace(*tokenKind)) {
		case "access":
			bearer = strings.TrimSpace(token.AccessToken)
		default:
			bearer = strings.TrimSpace(token.IDToken)
		}
		if bearer == "" {
			fail(fmt.Errorf("selected %s token is empty", *tokenKind))
		}
		params := json.RawMessage([]byte(`{}`))
		if strings.TrimSpace(*callParams) != "" {
			params = json.RawMessage([]byte(strings.TrimSpace(*callParams)))
		}
		if !json.Valid(params) {
			fail(fmt.Errorf("call params are not valid json"))
		}
		if err := issueJSONRPC(strings.TrimSpace(*callURL), strings.TrimSpace(*callMethod), params, bearer); err != nil {
			fail(err)
		}
	}
	if strings.TrimSpace(*sessionBaseURL) != "" {
		var bearer string
		switch strings.ToLower(strings.TrimSpace(*tokenKind)) {
		case "access":
			bearer = strings.TrimSpace(token.AccessToken)
		default:
			bearer = strings.TrimSpace(token.IDToken)
		}
		if bearer == "" {
			fail(fmt.Errorf("selected %s token is empty", *tokenKind))
		}
		if strings.TrimSpace(*a2aAgentID) == "" {
			fail(fmt.Errorf("--a2a-agent-id is required when --session-base-url is set"))
		}
		params := json.RawMessage([]byte(`{}`))
		if strings.TrimSpace(*callParams) != "" {
			params = json.RawMessage([]byte(strings.TrimSpace(*callParams)))
		}
		if !json.Valid(params) {
			fail(fmt.Errorf("call params are not valid json"))
		}
		if err := issueSessionA2A(strings.TrimSpace(*sessionBaseURL), strings.TrimSpace(*a2aAgentID), params, bearer); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err.Error())
	os.Exit(1)
}

func loadRow(ctx context.Context, db *sql.DB, userID, provider string) (*tokenRow, error) {
	query := `SELECT user_id, provider, enc_token, created_at, COALESCE(updated_at,''), version, refresh_status
		FROM user_oauth_token
		WHERE user_id = ? AND provider = ?`
	out := &tokenRow{}
	err := db.QueryRowContext(ctx, query, userID, provider).
		Scan(&out.UserID, &out.Provider, &out.EncToken, &out.CreatedAt, &out.UpdatedAt, &out.Version, &out.RefreshState)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func decryptToken(ctx context.Context, salt, enc string) (*struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresAt    time.Time
}, error) {
	raw, err := base64RawURLDecode(enc)
	if err != nil {
		return nil, err
	}
	key := &kms.Key{Kind: "raw", Raw: string(blowfish.EnsureKey([]byte(salt)))}
	dec, err := cipher.Decrypt(ctx, key, raw)
	if err != nil {
		return nil, err
	}
	var payload encToken
	if err := json.Unmarshal(dec, &payload); err != nil {
		return nil, err
	}
	out := &struct {
		AccessToken  string
		RefreshToken string
		IDToken      string
		ExpiresAt    time.Time
	}{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		IDToken:      payload.IDToken,
	}
	if strings.TrimSpace(payload.ExpiresAt) != "" {
		if ts, err := time.Parse(time.RFC3339, payload.ExpiresAt); err == nil {
			out.ExpiresAt = ts
		}
	}
	return out, nil
}

func summarizeJWT(token string) jwtSummary {
	token = strings.TrimSpace(token)
	if token == "" {
		return jwtSummary{}
	}
	s := jwtSummary{
		Present: true,
		FP:      fingerprint(token),
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return s
	}
	claims, err := decodeSegment(parts[1])
	if err != nil {
		return s
	}
	s.Sub = claimString(claims["sub"])
	s.Email = claimString(claims["email"])
	s.Iss = claimString(claims["iss"])
	s.Aud = claimStrings(claims["aud"])
	s.Azp = claimString(claims["azp"])
	s.Exp = claimUnix(claims["exp"])
	s.Iat = claimUnix(claims["iat"])
	return s
}

func printSummary(label string, s jwtSummary) {
	fmt.Printf("%s Present: %v\n", label, s.Present)
	if !s.Present {
		return
	}
	fmt.Printf("%s Fingerprint: %s\n", label, s.FP)
	fmt.Printf("%s sub: %s\n", label, emptyDash(s.Sub))
	fmt.Printf("%s email: %s\n", label, emptyDash(s.Email))
	fmt.Printf("%s iss: %s\n", label, emptyDash(s.Iss))
	fmt.Printf("%s aud: %s\n", label, emptyDash(s.Aud))
	fmt.Printf("%s azp: %s\n", label, emptyDash(s.Azp))
	fmt.Printf("%s exp: %s\n", label, emptyDash(s.Exp))
	fmt.Printf("%s iat: %s\n", label, emptyDash(s.Iat))
}

func emptyDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}

func fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

func decodeSegment(seg string) (map[string]interface{}, error) {
	switch len(seg) % 4 {
	case 2:
		seg += "=="
	case 3:
		seg += "="
	}
	data, err := base64.URLEncoding.DecodeString(seg)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func claimString(v interface{}) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func claimStrings(v interface{}) string {
	switch actual := v.(type) {
	case string:
		return strings.TrimSpace(actual)
	case []interface{}:
		items := make([]string, 0, len(actual))
		for _, item := range actual {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				items = append(items, strings.TrimSpace(s))
			}
		}
		return strings.Join(items, ",")
	default:
		return ""
	}
}

func claimUnix(v interface{}) string {
	switch actual := v.(type) {
	case float64:
		return time.Unix(int64(actual), 0).UTC().Format(time.RFC3339)
	case int64:
		return time.Unix(actual, 0).UTC().Format(time.RFC3339)
	case json.Number:
		if n, err := actual.Int64(); err == nil {
			return time.Unix(n, 0).UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func base64RawURLDecode(value string) ([]byte, error) {
	raw := strings.TrimSpace(value)
	switch len(raw) % 4 {
	case 2:
		raw += "=="
	case 3:
		raw += "="
	}
	return base64.URLEncoding.DecodeString(raw)
}

func issueJSONRPC(url, method string, params json.RawMessage, bearer string) error {
	body := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
	}
	if len(params) > 0 {
		body["params"] = json.RawMessage(params)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	fmt.Println()
	fmt.Printf("HTTP Status: %s\n", resp.Status)
	fmt.Printf("Response Headers:\n")
	for k, values := range resp.Header {
		fmt.Printf("  %s: %s\n", k, strings.Join(values, ", "))
	}
	fmt.Println("Response Body:")
	fmt.Println(string(respBody))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("request failed with status %s", resp.Status)
	}
	return nil
}

func issueSessionA2A(baseURL, agentID string, params json.RawMessage, bearer string) error {
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	sessionReq, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/api/auth/session", strings.NewReader(`{}`))
	if err != nil {
		return err
	}
	sessionReq.Header.Set("Content-Type", "application/json")
	sessionReq.Header.Set("Authorization", "Bearer "+bearer)
	sessionResp, err := client.Do(sessionReq)
	if err != nil {
		return err
	}
	defer sessionResp.Body.Close()
	sessionBody, _ := io.ReadAll(sessionResp.Body)
	fmt.Println()
	fmt.Printf("Session HTTP Status: %s\n", sessionResp.Status)
	fmt.Printf("Session Response Headers:\n")
	for k, values := range sessionResp.Header {
		fmt.Printf("  %s: %s\n", k, strings.Join(values, ", "))
	}
	fmt.Println("Session Response Body:")
	fmt.Println(string(sessionBody))
	if sessionResp.StatusCode >= 400 {
		return fmt.Errorf("session bootstrap failed with status %s", sessionResp.Status)
	}

	reqBody := map[string]interface{}{}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &reqBody); err != nil {
			return err
		}
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/v1/api/a2a/agents/"+agentID+"/message", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	fmt.Println()
	fmt.Printf("Shared A2A HTTP Status: %s\n", resp.Status)
	fmt.Printf("Shared A2A Response Headers:\n")
	for k, values := range resp.Header {
		fmt.Printf("  %s: %s\n", k, strings.Join(values, ", "))
	}
	fmt.Println("Shared A2A Response Body:")
	fmt.Println(string(respBody))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("shared a2a call failed with status %s", resp.Status)
	}
	return nil
}
