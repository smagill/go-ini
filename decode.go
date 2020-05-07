package ini

import (
	"reflect"
	"strconv"
)

// An UnmarshalTypeError describes a value that was not appropriate for a value
// of a specific Go type.
type UnmarshalTypeError struct {
	val string       // description of value - "bool", "array", "number -5"
	typ reflect.Type // type of Go value it could not be assigned to
	str string       // name of the struct type containing the field
	fld string       // name of the field within the struct
}

func (e *UnmarshalTypeError) Error() string {
	if e.str != "" || e.fld != "" {
		return "ini: cannot unmarshal " + e.val + " into Go struct field " + e.str + "." + e.fld + " of type " + e.typ.String()
	}
	return "ini: cannot unmarshal " + e.val + " into Go value of type " + e.typ.String()
}

// Unmarshal parses the INI-encoded data and stores the result in the value
// pointed to by v. If v is nil or not a pointer to a struct, Unmarshal returns
// an UnmarshalTypeError; INI-encoded data must be encoded into a struct.
//
// Unmarshal uses the inverse of the encodings that Marshal uses, following the
// rules below:
//
// So-called "global" property keys are matched to a struct field within v,
// either by its field name or tag. Values are then decoded according to the
// type of the destination field.
//
// Sections must be unmarshaled into a struct. Unmarshal matches the section
// name to a struct field name or tag. Subsequent property keys are then matched
// against struct field names or tags within the struct.
//
// If a duplicate section name or property key is encountered, Unmarshal will
// allocate a slice according to the number of duplicate keys found, and append
// each value to the slice. If the destination struct field is not a slice type,
// an error is returned.
//
// A struct field tag name may a single asterisk (colloquially known as the
// "wildcard" character). If such a tag is detected and the destination
// field is a slice of structs, all sections are decoded into the destination
// field as an element in the slice. If a struct field named "ININame" is
// encountered, the section name decoded into that field.
//
// A struct field tag containing "omitempty" will set the destination field to
// its type's zero value if no corresponding property key was encountered.
func Unmarshal(data []byte, v interface{}) error {
	return unmarshal(data, v, Options{})
}

// UnmarshalWithOptions allows parsing behavior to be configured with an Options
// value.
func UnmarshalWithOptions(data []byte, v interface{}, opts Options) error {
	return unmarshal(data, v, opts)
}

func unmarshal(data []byte, v interface{}, opts Options) error {
	p := newParser(data)
	p.l.opts.allowMultilineEscapeNewline = opts.AllowMultilineValues
	p.l.opts.allowMultilineWhitespacePrefix = opts.AllowMultilineValues
	p.l.opts.allowNumberSignComments = opts.AllowNumberSignComments
	if err := p.parse(); err != nil {
		return err
	}

	if err := decode(p.tree, reflect.ValueOf(v)); err != nil {
		return err
	}

	return nil
}

// decode sets the underlying values of the value to which rv points to the
// concrete value stored in the corresponding field of ast.
func decode(tree parseTree, rv reflect.Value) error {
	if rv.Type().Kind() != reflect.Ptr {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(tree).String(),
			typ: rv.Type(),
		}
	}

	rv = reflect.Indirect(rv)
	if rv.Type().Kind() != reflect.Struct {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(tree).String(),
			typ: rv.Type(),
		}
	}

	/* global properties */
	if err := decodeStruct(tree.global, rv.Addr()); err != nil {
		return err

	}

	for i := 0; i < rv.NumField(); i++ {
		sf := rv.Type().Field(i)
		sv := rv.Field(i).Addr()

		t := newTag(sf)
		if t.name == "-" {
			continue
		}

		switch sf.Type.Kind() {
		case reflect.Struct:
			sectionGroup, err := tree.get(t.name)
			if err != nil {
				return err
			}
			if len(sectionGroup) == 0 {
				continue
			}
			val := sectionGroup[0]
			if err := decodeStruct(val, sv); err != nil {
				return err
			}
		case reflect.Slice:
			if sf.Type.Elem().Kind() != reflect.Struct {
				continue
			}
			val, err := tree.get(t.name)
			if err != nil {
				return err
			}
			if len(val) == 0 {
				continue
			}
			if err := decodeSlice(val, sv); err != nil {
				return err
			}
		}
	}

	return nil
}

// decodeStruct sets the underlying values of the fields of the value to which
// rv points to the concrete values stored in i. If rv is not a reflect.Ptr,
// decodeStruct returns UnmarshalTypeError.
func decodeStruct(i interface{}, rv reflect.Value) error {
	if reflect.TypeOf(i) != reflect.TypeOf(section{}) || rv.Type().Kind() != reflect.Ptr {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	s := i.(section)
	rv = rv.Elem()

	for i := 0; i < rv.NumField(); i++ {
		sf := rv.Type().Field(i)
		sv := rv.Field(i).Addr()

		t := newTag(sf)
		if t.name == "-" {
			continue
		}

		switch sf.Type.Kind() {
		case reflect.Slice:
			// slices of structs inside a struct is *im-parsable*... get it?
			if sf.Type.Elem().Kind() == reflect.Struct {
				// TODO: This should probably error instead of silently skipping
				continue
			}

			prop, err := s.get(t.name)
			if err != nil {
				return err
			}
			val := prop.get("")
			if len(val) == 0 {
				continue
			}
			if err := decodeSlice(val, sv); err != nil {
				return err
			}
		case reflect.Map:
			if sf.Type.Elem().Kind() == reflect.Struct {
				continue
			}

			prop, err := s.get(t.name)
			if err != nil {
				return err
			}
			var val interface{}
			val = *prop
			if err := decodeMap(val, sv); err != nil {
				return err
			}
		case reflect.String:
			var val string
			if sf.Name == "ININame" {
				val = s.name
			} else {
				prop, err := s.get(t.name)
				if err != nil {
					return err
				}
				if len(prop.vals) == 0 {
					continue
				}
				vals := prop.get("")
				val = vals[0]
			}
			if err := decodeString(val, sv); err != nil {
				return err
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			prop, err := s.get(t.name)
			if err != nil {
				return err
			}
			if len(prop.vals) == 0 {
				continue
			}
			vals := prop.get("")
			val := vals[0]
			if err := decodeInt(val, sv); err != nil {
				return err
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			prop, err := s.get(t.name)
			if err != nil {
				return err
			}
			if len(prop.vals) == 0 {
				continue
			}
			vals := prop.get("")
			val := vals[0]
			if err := decodeUint(val, sv); err != nil {
				return err
			}
		case reflect.Float32, reflect.Float64:
			prop, err := s.get(t.name)
			if err != nil {
				return err
			}
			if len(prop.vals) == 0 {
				continue
			}
			vals := prop.get("")
			val := vals[0]
			if err := decodeFloat(val, sv); err != nil {
				return err
			}
		case reflect.Bool:
			prop, err := s.get(t.name)
			if err != nil {
				return err
			}
			if len(prop.vals) == 0 {
				continue
			}
			vals := prop.get("")
			val := vals[0]
			if err := decodeBool(val, sv); err != nil {
				return err
			}
		}
	}

	return nil
}

// decodeSlice sets the underlying values of the elements of the value to which
// rv points to the concrete values stored in i.
func decodeSlice(v interface{}, rv reflect.Value) error {
	if reflect.TypeOf(v).Kind() != reflect.Slice || rv.Type().Kind() != reflect.Ptr {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(v).String(),
			typ: rv.Type(),
		}
	}

	rv = rv.Elem()

	var decoderFunc func(interface{}, reflect.Value) error

	switch rv.Type().Elem().Kind() {
	case reflect.String:
		decoderFunc = decodeString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		decoderFunc = decodeInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		decoderFunc = decodeUint
	case reflect.Struct:
		decoderFunc = decodeStruct
	case reflect.Float32, reflect.Float64:
		decoderFunc = decodeFloat
	case reflect.Bool:
		decoderFunc = decodeBool
	default:
		return &UnmarshalTypeError{
			val: reflect.ValueOf(v).String(),
			typ: rv.Type(),
		}
	}

	vv := reflect.MakeSlice(rv.Type(), reflect.ValueOf(v).Len(), reflect.ValueOf(v).Cap())

	for i := 0; i < vv.Len(); i++ {
		sv := vv.Index(i).Addr()
		val := reflect.ValueOf(v).Index(i).Interface()
		if err := decoderFunc(val, sv); err != nil {
			return err
		}
	}

	rv.Set(vv)

	return nil
}

// decodeMap sets the underlying values of the elements of the value to which
// rv points to the concrete values stored in i.
func decodeMap(i interface{}, rv reflect.Value) error {
	if reflect.TypeOf(i) != reflect.TypeOf(property{}) || rv.Type().Kind() != reflect.Ptr {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	p := i.(property)
	rv = rv.Elem()

	var decoderFunc func(interface{}, reflect.Value) error

	switch rv.Type().Elem().Kind() {
	case reflect.String:
		decoderFunc = decodeString
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		decoderFunc = decodeInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		decoderFunc = decodeUint
	case reflect.Float32, reflect.Float64:
		decoderFunc = decodeFloat
	case reflect.Bool:
		decoderFunc = decodeBool
	default:
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	vv := reflect.MakeMap(rv.Type())

	for k, v := range p.vals {
		mv := reflect.New(rv.Type().Elem())
		var val interface{}
		if rv.Type().Elem().Kind() == reflect.Slice {
			val = v
		} else {
			val = v[0]
		}
		if err := decoderFunc(val, mv); err != nil {
			return err
		}

		vv.SetMapIndex(reflect.ValueOf(k), mv.Elem())
	}

	rv.Set(vv)

	return nil
}

// decodeString sets the underlying value of the value to which rv points to
// the concrete value stored in i. If rv is not a reflect.Ptr, decodeString
// returns UnmarshalTypeError.
func decodeString(i interface{}, rv reflect.Value) error {
	if reflect.TypeOf(i).Kind() != reflect.String || rv.Type().Kind() != reflect.Ptr {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	rv.Elem().SetString(i.(string))
	return nil
}

// decodeInt sets the underlying value of the value to which rv points to the
// concrete value stored in i. If rv is not a reflect.Ptr, decodeInt returns
// UnmarshalTypeError.
func decodeInt(i interface{}, rv reflect.Value) error {
	if reflect.TypeOf(i).Kind() != reflect.String || rv.Type().Kind() != reflect.Ptr {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	n, err := strconv.ParseInt(i.(string), 10, 64)
	if err != nil {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	rv.Elem().SetInt(n)
	return nil
}

// decodeUint sets the underlying value of the value to which rv points to the
// concrete value stored in i. If rv is not a reflect.Ptr, decodeUint returns
// UnmarshalTypeError.
func decodeUint(i interface{}, rv reflect.Value) error {
	if reflect.TypeOf(i).Kind() != reflect.String || rv.Type().Kind() != reflect.Ptr {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	n, err := strconv.ParseUint(i.(string), 10, 64)
	if err != nil {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	rv.Elem().SetUint(n)
	return nil
}

// decodeBool sets the underlying value of the value to which rv points to the
// concrete value stored in i. If rv is not a reflect.Ptr, decodeBool returns
// UnmarshalTypeError.
func decodeBool(i interface{}, rv reflect.Value) error {
	if reflect.TypeOf(i).Kind() != reflect.String || rv.Type().Kind() != reflect.Ptr {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	n, err := strconv.ParseBool(i.(string))
	if err != nil {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	rv.Elem().SetBool(n)
	return nil
}

// decodeFloat sets the underlying value of the value to which rv points to the
// concrete value stored in i. If rv is not a reflect.Ptr, decodeFloat returns
// UnmarshalTypeError.
func decodeFloat(i interface{}, rv reflect.Value) error {
	if reflect.TypeOf(i).Kind() != reflect.String || rv.Type().Kind() != reflect.Ptr {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	n, err := strconv.ParseFloat(i.(string), 64)
	if err != nil {
		return &UnmarshalTypeError{
			val: reflect.ValueOf(i).String(),
			typ: rv.Type(),
		}
	}

	rv.Elem().SetFloat(n)
	return nil
}
