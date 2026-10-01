package main

import (
	"context"
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	codec "github.com/viant/agently-core/internal/datly/codec"
	convbase "github.com/viant/agently-core/internal/datly/conversation/base"
	convread "github.com/viant/agently-core/internal/datly/conversation/read"
	msgbase "github.com/viant/agently-core/internal/datly/message/base"
	msgread "github.com/viant/agently-core/internal/datly/message/read"
	predicate "github.com/viant/agently-core/internal/datly/predicate"
	schedbase "github.com/viant/agently-core/internal/datly/schedule/base"
	schedread "github.com/viant/agently-core/internal/datly/schedule/read"
	"github.com/viant/bindly/resource"
	"github.com/viant/datly/bootstrap"
	"github.com/viant/datly/spec"
	"github.com/viant/datly/tag"
	"github.com/viant/datly/transcribe"
	"github.com/viant/datly/transcribe/column"
	"github.com/viant/datly/typecatalog"
	"github.com/viant/x"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

func main() {
	if len(os.Args) != 4 {
		panic("usage: datlybase <Core root> <read-only discovery SQLite file> <conversation|message|schedule>")
	}
	root := os.Args[1]
	entity := os.Args[3]
	db, err := sql.Open("sqlite3", "file:"+os.Args[2]+"?mode=ro")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	{
		if entity != "conversation" && entity != "message" && entity != "schedule" {
			panic("unsupported base reader")
		}
		base := filepath.Join(root, "dql", entity, "base")

		path := filepath.Join(base, "reader.dql")
		text, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		store := resource.New()
		if err = store.Register("canonical", os.DirFS(filepath.Join(root, "dql", entity, "read"))); err != nil {
			panic(err)
		}
		catalog := typecatalog.NewCatalog()
		for _, typ := range []reflect.Type{reflect.TypeFor[predicate.ConversationSearch](), reflect.TypeFor[predicate.MessageTurnTask](), reflect.TypeFor[predicate.MessageAssistantFinal](), reflect.TypeFor[predicate.MessageAssistantStatus](), reflect.TypeFor[codec.JSON]()} {
			if err = catalog.Register(typecatalog.TypeOriginPackage, x.NewType(typ)); err != nil {
				panic(err)
			}
		}
		inputTypes := map[string]reflect.Type{"conversation": reflect.TypeFor[convread.ConversationInput](), "message": reflect.TypeFor[msgread.MessagesInput](), "schedule": reflect.TypeFor[schedread.ScheduleInput]()}
		source := &transcribe.Source{LinkedInputType: inputTypes[entity], Name: "reader", Scope: "github.com/viant/agently-core/dql/" + entity + "/base", Path: path, Text: string(text), Resources: store, Types: catalog, Connector: "agently", ColumnRefiner: column.New(column.Connections{"agently": db})}
		outputTypes := map[string]reflect.Type{"conversation": reflect.TypeFor[convbase.ConversationOutput](), "message": reflect.TypeFor[msgbase.MessagesOutput](), "schedule": reflect.TypeFor[schedbase.ScheduleOutput]()}
		input := inputTypes[entity]
		output := outputTypes[entity]
		source.Scope = output.PkgPath()
		expected := "github.com/viant/agently-core/internal/datly/" + entity + "/base"
		if source.Scope != expected || !strings.Contains(string(text), "#package('"+expected+"')") {
			panic("base reader destination mismatch")
		}
		route := &bootstrap.RouteSource{HolderType: "BaseReader", FieldName: "Contract", PackagePath: source.Scope, Dir: filepath.Join(root, "internal/datly", entity, "base"), InputType: "canonical." + input.Name(), OutputType: output.Name(), Imports: []spec.ImportSpec{{Alias: "canonical", Package: input.PkgPath()}}, Tag: tag.Component{Name: "reader", Method: "GET", Path: "/v1/internal/agently/" + entity + "/base", Connector: "agently"}}
		compilation := &transcribe.PackageCompilation{Source: source, Component: route, InputType: input, OutputType: output, Types: catalog}
		result, err := compilation.Transcribe(context.Background(), root)
		if err != nil {
			panic(err)
		}
		fmt.Println(entity, "PASS", len(result.Result.Files))
	}
}
