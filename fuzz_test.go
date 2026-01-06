package lite3

import (
	"slices"
	"testing"
)

func FuzzValidate(f *testing.F) {
	// Add valid buffers as seeds
	b := New(nil)
	b.SetRootObject()
	f.Add(b.Bytes())

	b = New(nil)
	b.SetRootArray()
	f.Add(b.Bytes())

	b = New(nil)
	o := b.SetRootObject()
	o.Set(Key("foo"), String("bar"))
	o.Set(Key("num"), Int(42))
	o.Set(Key("flag"), Bool(true))
	f.Add(b.Bytes())

	b = New(nil)
	a := b.SetRootArray()
	a.Set(0, Int(1))
	a.Set(1, Int(2))
	a.Set(2, Int(3))
	f.Add(b.Bytes())

	// Nested object
	b = New(nil)
	o = b.SetRootObject()
	inner := o.SetObject(Key("inner"))
	inner.Set(Key("x"), Int(1))
	f.Add(b.Bytes())

	// Buffer with enough entries to cause a split
	b = New(nil)
	a = b.SetRootArray()
	for i := range 20 {
		a.Set(i, Int(i))
	}
	f.Add(b.Bytes())

	f.Fuzz(func(t *testing.T, data []byte) {
		// Copy the data since Open wraps it directly and Set will modify it
		buf, err := Open(slices.Clone(data))
		if err != nil {
			return
		}

		// If validation passed, we should be able to use the buffer without panicking
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic after successful validation: %v", r)
			}
		}()

		// iterate over all values
		it := buf.rootContainer().iter()
		var key string
		var index int
		var val Value
		for it.next(&key, &index, &val) {
			_ = val.Any()
		}

		// insert a new value
		switch r := buf.Root().(type) {
		case Object:
			r.Set(Key("_fuzz_test"), Int(123))
		case Array:
			r.Set(r.Len(), Int(456))
		}
	})
}
