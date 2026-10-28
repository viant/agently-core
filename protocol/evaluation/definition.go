package evaluation

import primitive "github.com/viant/forge/backend/provider/contract"

// Definition is the shared authored descriptor. Reads never initiate a run.
type Definition = primitive.EvalEnvelope
type ToolExpectation = primitive.EvalToolExpectation
type Dependency = primitive.EvalDependency
