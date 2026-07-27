package retrieve

import "reflect"

// DeepCopy returns an independent copy of v: pointers, slices, maps, arrays,
// structs, and interfaces are copied recursively rather than by reference,
// so mutating the result can never reach back into v. Scalars (numbers,
// strings, bools) pass through unchanged since they're already
// copy-by-value.
//
// Unexported struct fields are preserved by a whole-struct value copy but
// are not recursed into (reflect cannot address them), so a reference type
// held in an unexported field is not deep-copied. This is not a concern for
// the exported, JSON-tagged domain structs Retrieve is built for.
func DeepCopy[T any](v T) T {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return v
	}
	return deepCopyValue(rv).Interface().(T)
}

func deepCopyValue(rv reflect.Value) reflect.Value {
	switch rv.Kind() {
	case reflect.Ptr:
		if rv.IsNil() {
			return rv
		}
		cp := reflect.New(rv.Type().Elem())
		cp.Elem().Set(deepCopyValue(rv.Elem()))
		return cp
	case reflect.Interface:
		if rv.IsNil() {
			return rv
		}
		cp := reflect.New(rv.Type()).Elem()
		cp.Set(deepCopyValue(rv.Elem()))
		return cp
	case reflect.Slice:
		if rv.IsNil() {
			return rv
		}
		cp := reflect.MakeSlice(rv.Type(), rv.Len(), rv.Len())
		for i := 0; i < rv.Len(); i++ {
			cp.Index(i).Set(deepCopyValue(rv.Index(i)))
		}
		return cp
	case reflect.Array:
		cp := reflect.New(rv.Type()).Elem()
		for i := 0; i < rv.Len(); i++ {
			cp.Index(i).Set(deepCopyValue(rv.Index(i)))
		}
		return cp
	case reflect.Map:
		if rv.IsNil() {
			return rv
		}
		cp := reflect.MakeMapWithSize(rv.Type(), rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			cp.SetMapIndex(deepCopyValue(iter.Key()), deepCopyValue(iter.Value()))
		}
		return cp
	case reflect.Struct:
		cp := reflect.New(rv.Type()).Elem()
		cp.Set(rv) // whole-value copy first, so unexported fields survive
		for i := 0; i < rv.NumField(); i++ {
			f := rv.Field(i)
			if !f.CanInterface() {
				continue // unexported: shallow copy above already applied
			}
			cp.Field(i).Set(deepCopyValue(f))
		}
		return cp
	default:
		return rv
	}
}
