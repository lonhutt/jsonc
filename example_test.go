package jsonc_test

import (
	"fmt"

	"github.com/lonhutt/jsonc"
)

func ExampleParse() {
	text := `{
  // the server to talk to
  "host": "localhost",
  "port": 8080,
}`
	v, errs := jsonc.Parse(text, jsonc.AllowTrailingComma())
	fmt.Println(len(errs), v.(map[string]any)["port"])
	// Output: 0 8080
}

func ExampleParse_errors() {
	v, errs := jsonc.Parse(`{"a": 1 "b": 2}`)
	fmt.Println(v, errs)
	// Output: map[a:1 b:2] [CommaExpected at offset 8 (line 0, column 8)]
}

func ExampleParseTree() {
	root, _ := jsonc.ParseTree(`{"a": [1, 2]}`)
	node := jsonc.FindNodeAtLocation(root, jsonc.Path{"a", 1})
	fmt.Println(node.Literal, node.Offset)
	// Output: 2 10
}

func ExampleModify() {
	text := "{\n  // keep this\n  \"a\": 1\n}"
	o := jsonc.DefaultFormatOptions()
	o.TabSize = 2
	edits, err := jsonc.Modify(text, jsonc.Path{"b", "c"}, true, jsonc.ModifyOptions{Format: &o})
	if err != nil {
		panic(err)
	}
	out, err := jsonc.ApplyEdits(text, edits)
	if err != nil {
		panic(err)
	}
	fmt.Println(out)
	// Output:
	// {
	//   // keep this
	//   "a": 1,
	//   "b": {
	//     "c": true
	//   }
	// }
}

func ExampleRemove() {
	text := `{"a": 1, "b": [1, 2, 3]}`
	edits, err := jsonc.Remove(text, jsonc.Path{"b", 1}, jsonc.ModifyOptions{})
	if err != nil {
		panic(err)
	}
	out, _ := jsonc.ApplyEdits(text, edits)
	fmt.Println(out)
	// Output: {"a": 1, "b": [1, 3]}
}

func ExampleFormat() {
	text := `{"a":1,"b":[true,null]}`
	out, _ := jsonc.ApplyEdits(text, jsonc.Format(text, nil, jsonc.FormatOptions{TabSize: 2, InsertSpaces: true}))
	fmt.Println(out)
	// Output:
	// {
	//   "a": 1,
	//   "b": [
	//     true,
	//     null
	//   ]
	// }
}

func ExampleStripComments() {
	fmt.Println(jsonc.StripComments(`{"a": 1 /* note */}`))
	// Output: {"a": 1 }
}

func ExampleGetLocation() {
	loc := jsonc.GetLocation(`{"a": {"b": 1}}`, 12)
	fmt.Println(loc.Path, loc.Matches(jsonc.Path{"a", "*"}))
	// Output: [a b] true
}
