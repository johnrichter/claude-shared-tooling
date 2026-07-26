package retrieve

import (
	"reflect"
	"strings"
)

// FieldByTag returns the value of v's struct field whose `json:"name[,opts]"`
// tag matches field exactly (options ignored, "-" excluded; a field with no
// json tag falls back to its Go name). v may be a struct or a pointer to
// one. The result is deep-copied, so a caller mutating it cannot corrupt v.
//
// One generic accessor driven by v's own struct tags means an entity's
// retrievable field set is defined once, by its schema — never duplicated in
// a hand-maintained field-name switch that can drift out of sync with it.
func FieldByTag(v any, field string) (any, bool) {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil, false
	}
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)
		tag := sf.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = sf.Name
		}
		if name == "-" || name != field {
			continue
		}
		fv := rv.Field(i)
		if !fv.CanInterface() {
			return nil, false
		}
		return deepCopyValue(fv).Interface(), true
	}
	return nil, false
}
