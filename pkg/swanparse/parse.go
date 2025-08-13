package swanparse

import (
	"fmt"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

type SwanAST struct {
	Entry []*Entry `@@*`
}

type Entry struct {
	Command string  `@Ident`
	Name    string  `@Ident`
	Conn    []*Conn `"{" @@* "}"`
}

type Conn struct {
	Name string    `@Ident`
	Body []*Entity `"{" @@* "}"`
}

type Entity struct {
	Option *Option `  @@`
	Block  *Block  `| @@`
}

type Block struct {
	Name string    `@Ident`
	Body []*Entity `"{" @@* "}"`
}

type Option struct {
	Key   string `@Ident "="`
	Value *Value `@@`
}

type Value struct {
	Array  []*Value `  "[" ( @@ (@@)* )? "]"`
	Single *string  `| @Psk | @Ident`
}

func NewParser() (*participle.Parser[SwanAST], error) {
	lexer, err := lexer.NewSimple([]lexer.SimpleRule{
		{Name: "Psk", Pattern: `pre-shared key`},
		{Name: "Ident", Pattern: `[0-9_a-zA-Z\.\-/%]+`},
		{Name: "Whitespace", Pattern: `[ \t\r\n]+`},
		{Name: "Other", Pattern: `[-={}@\[\]\n\r]`},
	})
	if err != nil {
		return nil, fmt.Errorf("Error creating SwanAST parser: %w", err)
	}

	parser, err := participle.Build[SwanAST](
		participle.Lexer(lexer),
		participle.Elide("Whitespace"),
	)
	if err != nil {
		return nil, fmt.Errorf("Error creating SwanAST parser: %w", err)
	}
	return parser, nil
}

func Parse(input string) (*SwanAST, error) {
	parser, err := NewParser()
	if err != nil {
		return nil, err
	}
	ast, err := parser.ParseString("SwanAST", input)
	if err != nil {
		fmt.Printf("Error parsing string, error: %s\nstring: %s\n", err.Error(), input)
		return nil, err
	}
	return ast, nil
}
