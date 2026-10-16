// Package testmetadata holds a frozen framework artifact used to verify host
// source composition independently of any embedding application's UI bundle.
package testmetadata

import "embed"

// FS contains chat and schedule variants and their shared file browser dependency.
//
//go:embed window
var FS embed.FS
