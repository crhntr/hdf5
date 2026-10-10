package h5

import (
	"fmt"
	"math"
	"reflect"
	"strings"
)

// ConvertValues converts canonical values (see value.go) to a slice of T,
// following the table in API.md.
func ConvertValues[T any](vals []any) ([]T, error) {
	out := make([]T, len(vals))
	rt := reflect.TypeOf((*T)(nil)).Elem()
	for i, v := range vals {
		rv, err := convertValue(v, rt)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		out[i] = rv.Interface().(T)
	}
	return out, nil
}

// ConvertInto converts canonical values into *dst, which must be a pointer to
// a slice.
func ConvertInto(vals []any, dst any) error {
	pv := reflect.ValueOf(dst)
	if pv.Kind() != reflect.Pointer || pv.Elem().Kind() != reflect.Slice {
		return fmt.Errorf("hdf5: ReadInto destination must be a pointer to a slice, got %T", dst)
	}
	st := pv.Elem().Type()
	out := reflect.MakeSlice(st, len(vals), len(vals))
	for i, v := range vals {
		rv, err := convertValue(v, st.Elem())
		if err != nil {
			return fmt.Errorf("element %d: %w", i, err)
		}
		out.Index(i).Set(rv)
	}
	pv.Elem().Set(out)
	return nil
}

func convertValue(v any, rt reflect.Type) (reflect.Value, error) {
	if rt.Kind() == reflect.Interface && rt.NumMethod() == 0 {
		if v == nil {
			return reflect.Zero(rt), nil
		}
		return reflect.ValueOf(v).Convert(rt), nil
	}
	if v != nil && reflect.TypeOf(v) == rt {
		return reflect.ValueOf(v), nil
	}
	switch rt.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := toInt64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(rt).Elem()
		if out.OverflowInt(i) {
			return reflect.Value{}, fmt.Errorf("value %d overflows %s", i, rt)
		}
		out.SetInt(i)
		return out, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		u, err := toUint64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(rt).Elem()
		if out.OverflowUint(u) {
			return reflect.Value{}, fmt.Errorf("value %d overflows %s", u, rt)
		}
		out.SetUint(u)
		return out, nil
	case reflect.Float32, reflect.Float64:
		f, err := toFloat64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		out := reflect.New(rt).Elem()
		out.SetFloat(f)
		return out, nil
	case reflect.Bool:
		i, err := toInt64(v)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(i != 0).Convert(rt), nil
	case reflect.String:
		switch s := v.(type) {
		case string:
			return reflect.ValueOf(s).Convert(rt), nil
		case []byte:
			return reflect.ValueOf(string(s)).Convert(rt), nil
		case Enum:
			return reflect.ValueOf(s.Name).Convert(rt), nil
		}
	case reflect.Slice:
		if rt.Elem().Kind() == reflect.Uint8 {
			switch b := v.(type) {
			case []byte:
				return reflect.ValueOf(append([]byte(nil), b...)).Convert(rt), nil
			case string:
				return reflect.ValueOf([]byte(b)).Convert(rt), nil
			}
		}
		if elems, ok := v.([]any); ok {
			out := reflect.MakeSlice(rt, len(elems), len(elems))
			for i, e := range elems {
				ev, err := convertValue(e, rt.Elem())
				if err != nil {
					return reflect.Value{}, err
				}
				out.Index(i).Set(ev)
			}
			return out, nil
		}
	case reflect.Struct:
		c, ok := v.(Compound)
		if !ok {
			break
		}
		out := reflect.New(rt).Elem()
		for i := 0; i < rt.NumField(); i++ {
			sf := rt.Field(i)
			if !sf.IsExported() {
				continue
			}
			name := sf.Tag.Get("hdf5")
			if name == "-" {
				continue
			}
			if name == "" {
				name = sf.Name
			}
			fv, found := c.field(name)
			if !found {
				continue
			}
			cv, err := convertValue(fv, sf.Type)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("field %s: %w", sf.Name, err)
			}
			out.Field(i).Set(cv)
		}
		return out, nil
	}
	if v != nil && reflect.TypeOf(v).ConvertibleTo(rt) {
		return reflect.ValueOf(v).Convert(rt), nil
	}
	return reflect.Value{}, fmt.Errorf("cannot convert %T to %s", v, rt)
}

func (c Compound) field(name string) (any, bool) {
	for _, f := range c.Fields {
		if f.Name == name {
			return f.Value, true
		}
	}
	for _, f := range c.Fields {
		if strings.EqualFold(f.Name, name) {
			return f.Value, true
		}
	}
	return nil, false
}

func toInt64(v any) (int64, error) {
	switch x := v.(type) {
	case int8:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int64:
		return x, nil
	case uint8:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint64:
		if x > math.MaxInt64 {
			return 0, fmt.Errorf("value %d overflows int64", x)
		}
		return int64(x), nil
	case float32:
		return int64(x), nil
	case float64:
		return int64(x), nil
	case Enum:
		return x.Value, nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("cannot convert %T to integer", v)
}

func toUint64(v any) (uint64, error) {
	switch x := v.(type) {
	case uint8:
		return uint64(x), nil
	case uint16:
		return uint64(x), nil
	case uint32:
		return uint64(x), nil
	case uint64:
		return x, nil
	}
	i, err := toInt64(v)
	if err != nil {
		return 0, err
	}
	if i < 0 {
		return 0, fmt.Errorf("value %d is negative", i)
	}
	return uint64(i), nil
}

func toFloat64(v any) (float64, error) {
	switch x := v.(type) {
	case float32:
		return float64(x), nil
	case float64:
		return x, nil
	case uint64:
		return float64(x), nil
	}
	i, err := toInt64(v)
	if err != nil {
		return 0, fmt.Errorf("cannot convert %T to float", v)
	}
	return float64(i), nil
}
