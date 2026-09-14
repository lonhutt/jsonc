package jsonc

// Path identifies a value inside a JSON document. Each element is either a
// string (an object property name) or an int (an array index).
//
// A Path element of any other type is malformed. How that is reported depends
// on the function: Modify and Remove return an error, FindNodeAtLocation
// returns nil, and Location.Matches returns false.
type Path []any
