package sqlite

import (
	"fmt"
	"strings"
)

// Positions refer to the original SQL. Quoted identifiers, literals and comments
// must never be mistaken for constraint keywords or expression parentheses.
type ddlToken struct {
	start, end int
	text       string
	quoted     bool
}

func (t ddlToken) is(word string) bool { return !t.quoted && strings.EqualFold(t.text, word) }

func ddlTokens(sql string) ([]ddlToken, error) {
	var result []ddlToken
	for i := 0; i < len(sql); {
		start := i
		c := sql[i]
		switch {
		case strings.ContainsRune(" \t\r\n\f", rune(c)):
			i++
			continue
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated DDL comment")
			}
			i += end + 4
			continue
		case c == '\'' || c == '"' || c == '`' || c == '[':
			closing := c
			if c == '[' {
				closing = ']'
			}
			i++
			closed := false
			for i < len(sql) {
				if sql[i] != closing {
					i++
					continue
				}
				i++
				if c != '[' && i < len(sql) && sql[i] == closing {
					i++
					continue
				}
				closed = true
				break
			}
			if !closed {
				return nil, fmt.Errorf("unterminated DDL quote")
			}
			result = append(result, ddlToken{start, i, sql[start:i], true})
			continue
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 128:
			for i < len(sql) {
				x := sql[i]
				if !(x == '_' || x == '$' || x >= 'a' && x <= 'z' || x >= 'A' && x <= 'Z' || x >= '0' && x <= '9' || x >= 128) {
					break
				}
				i++
			}
		default:
			i++
		}
		result = append(result, ddlToken{start, i, sql[start:i], false})
	}
	return result, nil
}

// stripSQLiteChecks retains the installed schema, including extensions not
// present in the bootstrap DDL. Table constraints are removed as whole entries;
// column constraints are removed without changing the column definition.
func stripSQLiteChecks(sql string) (string, int, error) {
	tokens, err := ddlTokens(sql)
	if err != nil {
		return "", 0, err
	}
	open := -1
	for i, token := range tokens {
		if token.is("(") {
			open = i
			break
		}
	}
	if open < 0 {
		return sql, 0, nil
	}
	depth, start, count := 0, tokens[open].end, 0
	var parts []string
	for _, token := range tokens[open+1:] {
		if token.is(")") && depth == 0 || token.is(",") && depth == 0 {
			part, removed, err := stripSQLiteCheckEntry(sql[start:token.start])
			if err != nil {
				return "", 0, err
			}
			count += removed
			if strings.TrimSpace(part) != "" {
				parts = append(parts, part)
			}
			if token.is(")") {
				if count == 0 {
					return sql, 0, nil
				}
				if len(parts) == 0 {
					return "", 0, fmt.Errorf("CHECK removal leaves an empty table")
				}
				return sql[:tokens[open].end] + strings.Join(parts, ",") + sql[token.start:], count, nil
			}
			start = token.end
			continue
		}
		if token.is("(") {
			depth++
		} else if token.is(")") {
			depth--
		}
	}
	return "", 0, fmt.Errorf("unbalanced table DDL")
}

func stripSQLiteCheckEntry(sql string) (string, int, error) {
	tokens, err := ddlTokens(sql)
	if err != nil {
		return "", 0, err
	}
	var result strings.Builder
	last, depth, count := 0, 0, 0
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if depth == 0 && token.is("CHECK") && i+1 < len(tokens) && tokens[i+1].is("(") {
			begin := i
			if i >= 2 && tokens[i-2].is("CONSTRAINT") {
				begin = i - 2
			}
			// CHECK or CONSTRAINT name CHECK at the beginning is a table entry.
			if begin == 0 {
				return "", 1, nil
			}
			end, nesting := i+1, 0
			for ; end < len(tokens); end++ {
				if tokens[end].is("(") {
					nesting++
				} else if tokens[end].is(")") {
					nesting--
					if nesting == 0 {
						break
					}
				}
			}
			if end == len(tokens) {
				return "", 0, fmt.Errorf("unbalanced CHECK expression")
			}
			result.WriteString(sql[last:tokens[begin].start])
			last = tokens[end].end
			count++
			i = end
			continue
		}
		if token.is("(") {
			depth++
		} else if token.is(")") {
			depth--
		}
	}
	result.WriteString(sql[last:])
	return result.String(), count, nil
}

func sqliteReplacementDDL(sql, name string) (string, error) {
	tokens, err := ddlTokens(sql)
	if err != nil {
		return "", err
	}
	if len(tokens) < 4 || !tokens[0].is("CREATE") || !tokens[1].is("TABLE") {
		return "", fmt.Errorf("unsupported table DDL")
	}
	i := 2
	if tokens[i].is("IF") {
		i += 3
	}
	if i+1 < len(tokens) && tokens[i+1].is(".") {
		i += 2
	}
	if i >= len(tokens) || tokens[i].is("(") {
		return "", fmt.Errorf("missing table name")
	}
	return sql[:tokens[i].start] + quoteSQLiteIdentifier(name) + sql[tokens[i].end:], nil
}

func quoteSQLiteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
