// Package docgen generates an OpenRPC spec for the Celestia Node. It has been inspired by and adapted from Filecoin's
// Lotus API implementation.
package docgen

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"reflect"
	"strings"

	"github.com/alecthomas/jsonschema"
	go_openrpc_reflect "github.com/etclabscore/go-openrpc-reflect"
	meta_schema "github.com/open-rpc/meta-schema"
)

const (
	APIVersion     = "v0.0.1"
	APIDescription = "The Celestia Node API is the collection of RPC methods that " +
		"can be used to interact with the services provided by Celestia Data Availability Nodes."
	APIName  = "Celestia Node API"
	DocsURL  = "https://github.com/celestiaorg/celestia-node"
	DocsName = "Celestia Node GitHub"
)

type Visitor struct {
	Methods map[string]ast.Node
}

func (v *Visitor) Visit(node ast.Node) ast.Visitor {
	st, ok := node.(*ast.TypeSpec)
	if !ok {
		return v
	}

	if st.Name.Name != "Module" {
		return nil
	}

	iface := st.Type.(*ast.InterfaceType)
	for _, m := range iface.Methods.List {
		if len(m.Names) > 0 {
			v.Methods[m.Names[0].Name] = m
		}
	}

	return v
}

type Comments = map[string]string

func ParseCommentsFromNodebuilderModules(moduleNames ...string) Comments {
	fset := token.NewFileSet()
	nodeComments := make(Comments)
	for _, moduleName := range moduleNames {
		f, err := parser.ParseFile(fset, "nodebuilder/"+moduleName+"/service.go", nil, parser.AllErrors|parser.ParseComments)
		if err != nil {
			panic(err)
		}

		cmap := ast.NewCommentMap(fset, f, f.Comments)

		v := &Visitor{make(map[string]ast.Node)}
		ast.Walk(v, f)

		// TODO(@distractedm1nd): An issue with this could be two methods with the same name in different modules
		for mn, node := range v.Methods {
			filteredComments := cmap.Filter(node).Comments()
			if len(filteredComments) == 0 {
				nodeComments[mn] = "No comment exists yet for this method."
			} else {
				nodeComments[mn] = filteredComments[0].Text()
			}
		}
	}
	return nodeComments
}

func NewOpenRPCDocument(comments Comments) *go_openrpc_reflect.Document {
	d := &go_openrpc_reflect.Document{}

	d.WithMeta(&go_openrpc_reflect.MetaT{
		GetServersFn: func() func(listeners []net.Listener) (*meta_schema.Servers, error) {
			return func(listeners []net.Listener) (*meta_schema.Servers, error) {
				return nil, nil
			}
		},
		GetInfoFn: func() (info *meta_schema.InfoObject) {
			info = &meta_schema.InfoObject{}
			title := APIName
			info.Title = (*meta_schema.InfoObjectProperties)(&title)

			version := APIVersion
			info.Version = (*meta_schema.InfoObjectVersion)(&version)

			description := APIDescription
			info.Description = (*meta_schema.InfoObjectDescription)(&description)

			return info
		},
		GetExternalDocsFn: func() (exdocs *meta_schema.ExternalDocumentationObject) {
			url, description := DocsURL, DocsName

			return &meta_schema.ExternalDocumentationObject{
				Url:         (*meta_schema.ExternalDocumentationObjectUrl)(&url),
				Description: (*meta_schema.ExternalDocumentationObjectDescription)(&description),
			}
		},
	})

	appReflector := &go_openrpc_reflect.EthereumReflectorT{}

	appReflector.FnSchemaTypeMap = func() func(ty reflect.Type) *jsonschema.Type {
		return OpenRPCSchemaTypeMapper
	}

	appReflector.FnGetMethodExternalDocs = func(
		r reflect.Value,
		m reflect.Method,
		funcDecl *ast.FuncDecl,
	) (*meta_schema.ExternalDocumentationObject, error) {
		extDocs, err := go_openrpc_reflect.EthereumReflector.GetMethodExternalDocs(r, m, funcDecl)
		if err != nil {
			return nil, err
		}

		desc := "Source of the default service's implementation of this method."
		extDocs.Description = (*meta_schema.ExternalDocumentationObjectDescription)(&desc)

		url := strings.Replace(string(*extDocs.Url), "/master/", "/main/", 1)
		extDocs.Url = (*meta_schema.ExternalDocumentationObjectUrl)(&url)
		//
		return extDocs, nil
	}

	appReflector.FnIsMethodEligible = func(m reflect.Method) bool {
		// methods are only eligible if they were found in the Module interface
		_, ok := comments[m.Name]
		if !ok {
			return false
		}

		/* TODO(@distractedm1nd): find out why chans are excluded in lotus. is this a must?
		for i := 0; i < m.Func.Type().NumOut(); i++ {
			if m.Func.Type().Out(i).Kind() == reflect.Chan {
				return false
			}
		}
		*/
		return go_openrpc_reflect.EthereumReflector.IsMethodEligible(m)
	}

	// remove the default implementation from the method descriptions
	appReflector.FnGetMethodDescription = func(r reflect.Value, m reflect.Method, funcDecl *ast.FuncDecl) (string, error) {
		return "", nil // noComment
	}

	appReflector.FnGetMethodName = func(
		moduleName string,
		r reflect.Value,
		m reflect.Method,
		funcDecl *ast.FuncDecl,
	) (string, error) {
		return moduleName + "." + m.Name, nil
	}

	appReflector.FnGetMethodSummary = func(r reflect.Value, m reflect.Method, funcDecl *ast.FuncDecl) (string, error) {
		if v, ok := comments[m.Name]; ok {
			return v, nil
		}
		return "", nil // noComment
	}

	appReflector.FnSchemaExamples = func(ty reflect.Type) (examples *meta_schema.Examples, err error) {
		v, err := ExampleValue(ty, ty) // This isn't ideal, but seems to work well enough.
		if err != nil {
			fmt.Println(err)
		}
		return &meta_schema.Examples{
			meta_schema.AlwaysTrue(v),
		}, nil
	}

	d.WithReflector(appReflector)
	return d
}

const integerD = `{ "title": "number", "type": "number", "description": "Number is a number" }`

func OpenRPCSchemaTypeMapper(ty reflect.Type) *jsonschema.Type {
	unmarshalJSONToJSONSchemaType := func(input string) *jsonschema.Type {
		var js jsonschema.Type
		err := json.Unmarshal([]byte(input), &js)
		if err != nil {
			panic(err)
		}
		return &js
	}

	if ty.Kind() == reflect.Ptr {
		ty = ty.Elem()
	}

	if ty == reflect.TypeOf((*interface{})(nil)).Elem() {
		return &jsonschema.Type{Type: "object", AdditionalProperties: []byte("true")}
	}

	// Handle primitive types in case there are generic cases
	// specific to our services.
	switch ty.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// Return all integer types as the hex representation integer schemea.
		ret := unmarshalJSONToJSONSchemaType(integerD)
		return ret
	case reflect.Uintptr:
		return &jsonschema.Type{Type: "number", Title: "uintptr-title"}
	case reflect.Struct:
	case reflect.Map:
	case reflect.Slice, reflect.Array:
	case reflect.Float32, reflect.Float64:
	case reflect.Bool:
	case reflect.String:
	case reflect.Ptr, reflect.Interface:
	default:
	}

	return nil
}
