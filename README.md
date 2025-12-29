lite3
-----

[![GoDoc](https://godoc.org/lukechampine.com/lite3?status.svg)](https://godoc.org/lukechampine.com/lite3)

```
go get lukechampine.com/lite3
```

This repo contains a pure-Go implementation of [Lite³](https://lite3.io), a
zero-copy binary serialization format. Lite³ represents data as a B-tree inside
a single contiguous buffer, allowing access and mutation on any arbitrary field
in `O(log n)` time.

## Usage

The API is fairly low-level. There are two container types: `Object` and
`Array`, which each have `Set` methods for each supported type, and a `Value`
method for reading values of arbitrary type.

```go
import "lukechampine.com/lite3"
import "fmt"

func main() {
    b := lite3.New(nil)
    o := b.SetRootObject()
	o.SetBool("foo", true)
    fmt.Println(o.Value("foo").Bool()) // true
    
    // nested containers:
    a := o.SetArray("bar")
    a.SetString(0, "hello")
    a.SetString(1, "world")

    // iteration:
    it := o.Iter()
    fmt.Println(it.Next().Value) // <array>
    fmt.Println(it.Next().Value) // true

    // json compatibility:
    js, _ := b.MarshalJSON()
    fmt.Println(string(js)) // {"bar":["hello","world"],"foo":true}
    
    var b2 lite3.Buffer
    b2.UnmarshalJSON(js)
    fmt.Println(b2.Root().(lite3.Object).Value("foo")) // true
}
```

## Performance

...is currently underwhelming, because this implementation skews more towards
explicit encoding/decoding than unsafe casting. Should be possible to make it
much faster by assuming little-endian architecture.
