package raymond

import "log"

// requireParams reproduces callFunc's arity error: numIn is the legacy
// helper's reflected parameter count (including *Options if it had
// one); wantParams is the number of template-supplied params.
func requireParams(hc *HelperCall, numIn, wantParams int) error {
	if hc.NumParams() != wantParams {
		return hc.s.errorf("Helper '%s' called with wrong number of arguments, needed %d but got %d",
			hc.name, numIn, hc.NumParams())
	}
	return nil
}

// includableZero ports Options.isIncludableZero (helper.go:284-295).
func includableZero(hc *HelperCall) bool {
	if b, ok := hc.Hash("includeZero").Interface().(bool); ok && b {
		if nb, ok := hc.Param(0).Interface().(int); ok && nb == 0 {
			return true
		}
	}
	return false
}

// builtinIf ports ifHelper (helper.go:302-308), streaming.
func builtinIf(hc *HelperCall) error {
	if err := requireParams(hc, 2, 1); err != nil {
		return err
	}
	if includableZero(hc) || hc.Param(0).Truthy() {
		return hc.Fn()
	}
	return hc.Inverse()
}

// builtinUnless ports unlessHelper (helper.go:311-317), streaming.
func builtinUnless(hc *HelperCall) error {
	if err := requireParams(hc, 2, 1); err != nil {
		return err
	}
	if includableZero(hc) || hc.Param(0).Truthy() {
		return hc.Inverse()
	}
	return hc.Fn()
}

// builtinWith ports withHelper (helper.go:320-326), streaming.
func builtinWith(hc *HelperCall) error {
	if err := requireParams(hc, 2, 1); err != nil {
		return err
	}
	if hc.Param(0).Truthy() {
		return hc.fnWithKey(hc.Param(0), nil, nil)
	}
	return hc.Inverse()
}

// builtinEach ports eachHelper (helper.go:329-382), streaming each
// iteration instead of concatenating.
func builtinEach(hc *HelperCall) error {
	if err := requireParams(hc, 2, 1); err != nil {
		return err
	}
	ctx := hc.Param(0)
	if !ctx.Truthy() {
		return hc.Inverse()
	}

	it := iterableOf(ctx)
	if it == nil {
		return nil
	}
	length := it.Len()
	return it.Each(func(i int, key interface{}, val Value) error {
		if err := hc.s.step(1); err != nil {
			return err
		}
		// arrays: frame key nil, block-param key = index (helper.go:341-344)
		blockKey := key
		if blockKey == nil {
			blockKey = i
		}
		frame := hc.s.frame.newIterDataFrame(length, i, key)
		return hc.fnWithKey(val, frame, blockKey)
	})
}

// iterableOf extracts the Iterable behind a Value (reflectData and
// valueMap implement it; bare Lists iterate by index).
func iterableOf(v Value) Iterable {
	if v.data != nil {
		if it, ok := v.data.(Iterable); ok {
			return it
		}
	}
	if v.list != nil {
		return listIterable{l: v.list}
	}
	return nil
}

type listIterable struct{ l List }

func (li listIterable) Len() int { return li.l.Len() }

func (li listIterable) Each(fn func(i int, key interface{}, val Value) error) error {
	for i := 0; i < li.l.Len(); i++ {
		if err := fn(i, nil, li.l.Index(i)); err != nil {
			return err
		}
	}
	return nil
}

// builtinLog ports logHelper (helper.go:384-388).
func builtinLog(hc *HelperCall) error {
	if err := requireParams(hc, 1, 1); err != nil {
		return err
	}
	log.Print(hc.Param(0).Str())
	return nil
}

// builtinLookup ports lookupHelper (helper.go:391-393):
// Str(options.Eval(obj, field)), written to hc's writer.
func builtinLookup(hc *HelperCall) error {
	if err := requireParams(hc, 3, 2); err != nil {
		return err
	}
	obj := hc.Param(0)
	field := hc.Param(1).Str()
	if !obj.IsValid() || field == "" {
		return nil
	}
	v, err := hc.s.lookupField(obj, field, false)
	if err != nil {
		return err
	}
	if str := v.Str(); str != "" {
		_, werr := hc.WriteString(str)
		return werr
	}
	return nil
}

// builtinEqual ports equalHelper (helper.go:396-401).
func builtinEqual(hc *HelperCall) error {
	if err := requireParams(hc, 3, 2); err != nil {
		return err
	}
	if hc.Param(0).Str() == hc.Param(1).Str() {
		return hc.Fn()
	}
	return nil
}
