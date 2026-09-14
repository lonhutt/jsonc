// Package jsonc parses and edits JSON with comments (JSONC), the dialect used
// by VS Code settings and tsconfig.json files.
//
// Parsing is fault tolerant: Parse and ParseTree always return a best-effort
// result alongside a slice of ParseError describing what was wrong, so a
// caller can work with a partially valid document. Callers wanting strictness
// check len(errs) == 0.
//
// All offsets and lengths are byte offsets into the input string. This differs
// from the JavaScript original, which counts UTF-16 code units; the two agree
// for ASCII input. See the README for the full list of differences.
package jsonc
