package workspace

import old "github.com/viant/agently-core/workspace"

const (
	KindAgent            = old.KindAgent
	KindModel            = old.KindModel
	KindEmbedder         = old.KindEmbedder
	KindMCP              = old.KindMCP
	KindWorkflow         = old.KindWorkflow
	KindTool             = old.KindTool
	KindToolBundle       = old.KindToolBundle
	KindToolInstructions = old.KindToolInstructions
	KindOAuth            = old.KindOAuth
	KindFeeds            = old.KindFeeds
	KindA2A              = old.KindA2A
)

func AllKinds() []string                      { return old.AllKinds() }
func SetRoot(path string)                     { old.SetRoot(path) }
func Root() string                            { return old.Root() }
func RuntimeRoot() string                     { return old.RuntimeRoot() }
func StateRoot() string                       { return old.StateRoot() }
func SetRuntimeRoot(path string)              { old.SetRuntimeRoot(path) }
func SetStateRoot(path string)                { old.SetStateRoot(path) }
func ResolvePathTemplate(value string) string { return old.ResolvePathTemplate(value) }
func Path(kind string) string                 { return old.Path(kind) }
