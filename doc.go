// Package jsonc parses and edits JSON with comments (JSONC), the dialect VS
// Code settings and tsconfig.json use.
//
// Parsing is fault tolerant; Parse and ParseTree always return a best-effort
// result with a slice of ParseError, so check len(errs) == 0 to be strict.
//
// Offsets are bytes, where the JavaScript original counts UTF-16 code units;
// they agree for ASCII.
package jsonc
