package sdk

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

type mobileParityException struct {
	Surface  string `json:"surface"`
	Platform string `json:"platform"`
	Reason   string `json:"reason"`
	Expires  string `json:"expires"`
}

func TestMobileSDKPublicSurfacesCoverClientContract(t *testing.T) {
	required := clientContractMethods(t)
	exceptions := mobileParityExceptions(t, required)
	android := mobileMethodsFromFiles(t, "android",
		"android/src/main/java/com/viant/agentlysdk/Client.kt",
		"android/src/main/java/com/viant/agentlysdk/Lookups.kt",
	)
	ios := mobileMethodsFromFiles(t, "ios",
		"ios/Sources/AgentlySDK/AgentlyClient.swift",
		"ios/Sources/AgentlySDK/Lookups.swift",
	)

	assertMobileCoverage(t, "android", required, android, exceptions)
	assertMobileCoverage(t, "ios", required, ios, exceptions)
}

func clientContractMethods(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "client.go", nil, 0)
	if err != nil {
		t.Fatalf("parse client.go: %v", err)
	}
	var out []string
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Client" {
				continue
			}
			iface, ok := ts.Type.(*ast.InterfaceType)
			if !ok {
				t.Fatalf("Client is not an interface")
			}
			for _, field := range iface.Methods.List {
				for _, name := range field.Names {
					out = append(out, lowerCamel(name.Name))
				}
			}
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatalf("no Client interface methods found")
	}
	return out
}

func mobileParityExceptions(t *testing.T, required []string) map[string]mobileParityException {
	t.Helper()
	data, err := os.ReadFile("mobile_parity_exceptions.json")
	if err != nil {
		t.Fatalf("read mobile parity exceptions: %v", err)
	}
	var rows []mobileParityException
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("decode mobile parity exceptions: %v", err)
	}
	out := map[string]mobileParityException{}
	requiredSet := map[string]bool{}
	for _, method := range required {
		requiredSet[method] = true
	}
	today := time.Now().UTC()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	for _, row := range rows {
		surface := strings.TrimSpace(row.Surface)
		platform := strings.TrimSpace(row.Platform)
		if surface == "" || platform == "" || strings.TrimSpace(row.Reason) == "" || strings.TrimSpace(row.Expires) == "" {
			t.Fatalf("invalid mobile parity exception: %+v", row)
		}
		if platform != "android" && platform != "ios" {
			t.Fatalf("mobile parity exception has unsupported platform %q for %q", platform, surface)
		}
		if !requiredSet[surface] {
			t.Fatalf("mobile parity exception %s:%s does not reference sdk.Client", platform, surface)
		}
		expires, err := time.Parse("2006-01-02", row.Expires)
		if err != nil {
			t.Fatalf("mobile parity exception %s:%s has invalid expires %q: %v", platform, surface, row.Expires, err)
		}
		if expires.Before(today) {
			t.Fatalf("mobile parity exception %s:%s expired on %s", platform, surface, row.Expires)
		}
		key := platform + ":" + surface
		if _, ok := out[key]; ok {
			t.Fatalf("duplicate mobile parity exception for %s", key)
		}
		out[key] = row
	}
	return out
}

func mobileMethodsFromFiles(t *testing.T, platform string, paths ...string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s surface %s: %v", platform, path, err)
		}
		for _, method := range mobileMethodsForSource(platform, string(data)) {
			out[method] = true
		}
	}
	return out
}

func mobileMethodsForSource(platform, src string) []string {
	methods := map[string]bool{}
	switch platform {
	case "android":
		addRegexMethods(methods, src, regexp.MustCompile(`(?m)^\s*(?:public\s+)?(?:suspend\s+)?fun\s+AgentlyClient\.([A-Za-z][A-Za-z0-9_]*)\s*\(`))
		if body, ok := kotlinClassBody(src, "AgentlyClient"); ok {
			addRegexMethods(methods, body, regexp.MustCompile(`(?m)^\s*(?:public\s+)?(?:suspend\s+)?fun\s+([A-Za-z][A-Za-z0-9_]*)\s*\(`))
		}
	case "ios":
		re := regexp.MustCompile(`(?m)^\s*public\s+func\s+([A-Za-z][A-Za-z0-9_]*)\s*\(`)
		for _, body := range typeBodies(src, regexp.MustCompile(`(?:public\s+final\s+)?class\s+AgentlyClient\b[^{]*\{`)) {
			addRegexMethods(methods, body, re)
		}
		for _, body := range typeBodies(src, regexp.MustCompile(`extension\s+AgentlyClient\b[^{]*\{`)) {
			addRegexMethods(methods, body, re)
		}
	default:
		return nil
	}
	out := make([]string, 0, len(methods))
	for method := range methods {
		out = append(out, method)
	}
	sort.Strings(out)
	return out
}

func kotlinClassBody(src, name string) (string, bool) {
	start := strings.Index(src, "class "+name)
	if start < 0 {
		return "", false
	}
	parenDepth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '{':
			if parenDepth == 0 {
				return balancedBody(src, i)
			}
		}
	}
	return "", false
}

func addRegexMethods(out map[string]bool, src string, re *regexp.Regexp) {
	for _, match := range re.FindAllStringSubmatch(src, -1) {
		out[match[1]] = true
	}
}

func typeBodies(src string, re *regexp.Regexp) []string {
	matches := re.FindAllStringIndex(src, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		open := match[1] - 1
		body, ok := balancedBody(src, open)
		if ok {
			out = append(out, body)
		}
	}
	return out
}

func balancedBody(src string, open int) (string, bool) {
	if open < 0 || open >= len(src) || src[open] != '{' {
		return "", false
	}
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open+1 : i], true
			}
		}
	}
	return "", false
}

func assertMobileCoverage(t *testing.T, platform string, required []string, actual map[string]bool, exceptions map[string]mobileParityException) {
	t.Helper()
	var missing []string
	for _, method := range required {
		if actual[method] {
			continue
		}
		key := platform + ":" + method
		if _, ok := exceptions[key]; ok {
			continue
		}
		missing = append(missing, method)
	}
	if len(missing) > 0 {
		t.Fatalf("%s SDK is missing sdk.Client surfaces: %s", platform, strings.Join(missing, ", "))
	}
	for key, exception := range exceptions {
		if !strings.HasPrefix(key, platform+":") {
			continue
		}
		if actual[exception.Surface] {
			t.Fatalf("%s parity exception %q is stale; method is now present", platform, exception.Surface)
		}
	}
}

func lowerCamel(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(value[:1]) + value[1:]
}
