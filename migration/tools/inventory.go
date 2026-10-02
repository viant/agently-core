//go:build ignore

// inventory reads the legacy source without loading its dependencies.
package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type Field struct {
	Name, Type string
	Tags       map[string]string
}
type Shape struct {
	Name   string
	Fields []Field
}
type File struct {
	Path, Package string
	Imports       map[string]string
	Shapes        []Shape
	Constants     map[string]string
	SQLCalls      []string
	Functions     []string
}

func main() {
	root := os.Args[1]
	result := []File{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || entry.Name() == ".build" || entry.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		node, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		f := File{Path: filepath.ToSlash(rel), Package: node.Name.Name, Imports: map[string]string{}, Constants: map[string]string{}}
		render := func(expr ast.Node) string { var b bytes.Buffer; _ = format.Node(&b, fset, expr); return b.String() }
		for _, imp := range node.Imports {
			v, _ := strconv.Unquote(imp.Path.Value)
			alias := filepath.Base(v)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			f.Imports[alias] = v
		}
		ast.Inspect(node, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.TypeSpec:
				if s, ok := x.Type.(*ast.StructType); ok {
					shape := Shape{Name: x.Name.Name}
					for _, field := range s.Fields.List {
						tags := map[string]string{}
						if field.Tag != nil {
							raw, _ := strconv.Unquote(field.Tag.Value)
							tag := reflect.StructTag(raw)
							for _, key := range []string{"parameter", "view", "sql", "sqlx", "predicate", "codec", "value", "json", "on", "self", "validate", "internal", "querySelector"} {
								if v, ok := tag.Lookup(key); ok {
									tags[key] = v
								}
							}
						}
						for _, name := range field.Names {
							shape.Fields = append(shape.Fields, Field{Name: name.Name, Type: render(field.Type), Tags: tags})
						}
					}
					f.Shapes = append(f.Shapes, shape)
				}
			case *ast.ValueSpec:
				for j, name := range x.Names {
					if j < len(x.Values) {
						if lit, ok := x.Values[j].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							v, _ := strconv.Unquote(lit.Value)
							f.Constants[name.Name] = v
						}
					}
				}
			case *ast.FuncDecl:
				name := x.Name.Name
				if x.Recv != nil {
					name = render(x.Recv.List[0].Type) + "." + name
				}
				f.Functions = append(f.Functions, name)
			case *ast.CallExpr:
				if fn, ok := x.Fun.(*ast.SelectorExpr); ok {
					switch fn.Sel.Name {
					case "QueryContext", "QueryRowContext", "ExecContext", "Query", "QueryRow", "Exec", "Db", "AddHandler", "AddComponent":
						f.SQLCalls = append(f.SQLCalls, render(x.Fun))
					}
				}
			}
			return true
		})
		if len(f.Shapes) > 0 || len(f.SQLCalls) > 0 {
			result = append(result, f)
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err = enc.Encode(result); err != nil {
		panic(err)
	}
}
