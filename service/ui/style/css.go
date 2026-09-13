package style

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

// validateCSS parses syntax and resource references. Scope warnings are authoring
// diagnostics, not a sandbox: trusted workspace CSS remains document-wide.
func validateCSS(data []byte, themeID, mode string) ([]string, error) {
	lexer := css.NewLexer(parse.NewInputBytes(data))
	var brackets []css.TokenType
	for {
		tt, raw := lexer.Next()
		if tt == css.ErrorToken {
			if lexer.Err() != io.EOF {
				return nil, fmt.Errorf("invalid CSS")
			}
			break
		}
		switch tt {
		case css.BadStringToken, css.BadURLToken:
			return nil, fmt.Errorf("invalid CSS string or URL")
		case css.URLToken:
			return nil, fmt.Errorf("CSS resource URLs are not supported")
		case css.AtKeywordToken:
			if strings.EqualFold(unescapeCSS(string(raw)), "@import") {
				return nil, fmt.Errorf("CSS @import is not supported")
			}
		case css.FunctionToken:
			name := strings.ToLower(strings.TrimSuffix(unescapeCSS(string(raw)), "("))
			switch name {
			case "url", "image", "image-set", "-webkit-image-set", "src":
				return nil, fmt.Errorf("CSS resource functions are not supported")
			}
			brackets = append(brackets, css.RightParenthesisToken)
		case css.LeftBraceToken:
			brackets = append(brackets, css.RightBraceToken)
		case css.LeftBracketToken:
			brackets = append(brackets, css.RightBracketToken)
		case css.LeftParenthesisToken:
			brackets = append(brackets, css.RightParenthesisToken)
		case css.RightBraceToken, css.RightBracketToken, css.RightParenthesisToken:
			if len(brackets) == 0 || brackets[len(brackets)-1] != tt {
				return nil, fmt.Errorf("unbalanced CSS delimiters")
			}
			brackets = brackets[:len(brackets)-1]
		}
	}
	if len(brackets) != 0 {
		return nil, fmt.Errorf("unbalanced CSS delimiters")
	}
	parser := css.NewParser(parse.NewInputBytes(data), false)
	warnings := map[string]bool{}
	var atRules []string
	for {
		grammar, _, raw := parser.Next()
		switch grammar {
		case css.ErrorGrammar:
			if parser.Err() != io.EOF {
				return nil, fmt.Errorf("invalid CSS syntax")
			}
			result := []string{}
			for _, warning := range []string{"selector lacks .agently-workspace scope", "selector lacks its theme scope", "selector lacks its color-mode scope"} {
				if warnings[warning] {
					result = append(result, warning)
				}
			}
			return result, nil
		case css.BeginAtRuleGrammar:
			atRules = append(atRules, strings.ToLower(unescapeCSS(string(raw))))
		case css.EndAtRuleGrammar:
			if len(atRules) > 0 {
				atRules = atRules[:len(atRules)-1]
			}
		case css.QualifiedRuleGrammar, css.BeginRulesetGrammar:
			if len(atRules) > 0 && strings.HasSuffix(atRules[len(atRules)-1], "keyframes") {
				continue
			}
			selector := parser.Values()
			if !hasScopeClass(selector) {
				warnings["selector lacks .agently-workspace scope"] = true
			}
			if themeID != "" && !hasScopeAttribute(selector, "data-forge-theme", themeID) {
				warnings["selector lacks its theme scope"] = true
			}
			if mode != "" && !hasScopeAttribute(selector, "data-forge-color-mode", mode) {
				warnings["selector lacks its color-mode scope"] = true
			}
		}
	}
}
func hasScopeClass(tokens []css.Token) bool {
	for i := 1; i < len(tokens); i++ {
		if tokens[i-1].TokenType == css.DelimToken && bytes.Equal(tokens[i-1].Data, []byte(".")) && tokens[i].TokenType == css.IdentToken && unescapeCSS(string(tokens[i].Data)) == "agently-workspace" {
			return true
		}
	}
	return false
}
func hasScopeAttribute(tokens []css.Token, key, value string) bool {
	compact := make([]css.Token, 0, len(tokens))
	for _, t := range tokens {
		if t.TokenType != css.WhitespaceToken && t.TokenType != css.CommentToken {
			compact = append(compact, t)
		}
	}
	for i := 0; i+4 < len(compact); i++ {
		if compact[i].TokenType != css.LeftBracketToken || compact[i+1].TokenType != css.IdentToken || unescapeCSS(string(compact[i+1].Data)) != key || string(compact[i+2].Data) != "=" || compact[i+4].TokenType != css.RightBracketToken {
			continue
		}
		actual := unescapeCSS(strings.Trim(string(compact[i+3].Data), "\"'"))
		if actual == value {
			return true
		}
	}
	return false
}

// Decode identifier escapes after lexing; escaped @import/function names must
// have exactly the same resource policy as their ordinary spelling.
func unescapeCSS(value string) string {
	var result strings.Builder
	for i := 0; i < len(value); {
		if value[i] != '\\' {
			result.WriteByte(value[i])
			i++
			continue
		}
		i++
		start := i
		for i < len(value) && i-start < 6 && strings.ContainsRune("0123456789abcdefABCDEF", rune(value[i])) {
			i++
		}
		if i > start {
			code, _ := strconv.ParseUint(value[start:i], 16, 32)
			if code == 0 || code > utf8.MaxRune || (code >= 0xd800 && code <= 0xdfff) {
				result.WriteRune(utf8.RuneError)
			} else {
				result.WriteRune(rune(code))
			}
			if i < len(value) && strings.ContainsRune(" \t\n\r\f", rune(value[i])) {
				if value[i] == '\r' && i+1 < len(value) && value[i+1] == '\n' {
					i++
				}
				i++
			}
		} else if i < len(value) {
			result.WriteByte(value[i])
			i++
		}
	}
	return result.String()
}
