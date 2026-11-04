//go:build tools

package ent

// Keeps the code generator's dependencies in go.mod, since generate.go is
// excluded from builds.
import _ "entgo.io/ent/entc/gen"
