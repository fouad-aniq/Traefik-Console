// Command inspect_models records the public JSON fields of the core-v2 DTOs.
// It uses Go's parser, not imported application code or a runtime server.
package main

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type field struct {
	Type     string `json:"type"`
	Optional bool   `json:"optional"`
}

func main() {
	if len(os.Args) != 2 {
		panic("usage: inspect_models module-root")
	}
	root := os.Args[1]
	result := map[string]map[string]field{}
	fset := token.NewFileSet()
	e := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, e := parser.ParseFile(fset, path, nil, 0)
		if e != nil {
			return e
		}
		for _, decl := range source.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range group.Specs {
				named, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				model, ok := named.Type.(*ast.StructType)
				if !ok {
					continue
				}
				fields := map[string]field{}
				for _, f := range model.Fields.List {
					if f.Tag == nil {
						continue
					}
					tag, e := strconv.Unquote(f.Tag.Value)
					if e != nil {
						return e
					}
					value, present := reflect.StructTag(tag).Lookup("json")
					if !present || value == "-" {
						continue
					}
					parts := strings.Split(value, ",")
					var typeName bytes.Buffer
					if e = printer.Fprint(&typeName, fset, f.Type); e != nil {
						return e
					}
					fields[parts[0]] = field{Type: typeName.String(), Optional: strings.Contains(value, ",omitempty")}
				}
				if len(fields) > 0 {
					rel, _ := filepath.Rel(root, filepath.Dir(path))
					result[filepath.ToSlash(rel)+"."+named.Name.Name] = fields
				}
			}
		}
		return nil
	})
	if e != nil {
		panic(e)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if e = enc.Encode(result); e != nil {
		panic(e)
	}
}
