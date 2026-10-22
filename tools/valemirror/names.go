package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"unicode"
	"unicode/utf8"
)

// docNames returns the names that each doc comment of the Go source src may start with, by the
// line the comment starts on. A file that fails to parse gives those of the part that parses.
func docNames(src []byte) map[int][]string {
	fset := token.NewFileSet()
	mode := parser.ParseComments | parser.SkipObjectResolution
	file, _ := parser.ParseFile(fset, "", src, mode) //nolint:errcheck // a partial file will do
	if file == nil {
		return nil
	}
	names := make(map[int][]string)
	add := func(doc *ast.CommentGroup, idents ...*ast.Ident) {
		if doc == nil {
			return
		}
		line := fset.Position(doc.Pos()).Line
		for _, ident := range idents {
			names[line] = append(names[line], ident.Name)
		}
	}
	add(file.Doc, file.Name)
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			add(n.Doc, n.Name)
		case *ast.GenDecl:
			for _, spec := range n.Specs {
				add(n.Doc, specNames(spec)...)
			}
		case *ast.TypeSpec:
			add(n.Doc, n.Name)
		case *ast.ValueSpec:
			add(n.Doc, n.Names...)
		case *ast.Field:
			add(n.Doc, n.Names...)
		}
		return true
	})
	return names
}

// specNames returns the names a type, const or var spec declares.
func specNames(spec ast.Spec) []*ast.Ident {
	switch spec := spec.(type) {
	case *ast.TypeSpec:
		return []*ast.Ident{spec.Name}
	case *ast.ValueSpec:
		return spec.Names
	}
	return nil
}

// mask returns line with the name it starts with, alone or after an article or "Package", as an
// x per rune. Vale masks such a name too, as code that a rule such as TooWordy would misread.
func mask(line string, names []string) string {
	for _, lead := range []string{"", "A ", "An ", "The ", "Package "} {
		rest, ok := strings.CutPrefix(line, lead)
		if !ok {
			continue
		}
		for _, name := range names {
			after, ok := strings.CutPrefix(rest, name)
			if r, _ := utf8.DecodeRuneInString(after); ok && !identifier(r) {
				return lead + strings.Repeat("x", utf8.RuneCountInString(name)) + after
			}
		}
	}
	return line
}

// identifier returns whether r may continue a Go identifier.
func identifier(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
