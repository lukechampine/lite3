// Package lite3 is an implementation of the Lite³ serialization format.
package lite3

import (
	"encoding/binary"
	"errors"
	"math"
	"slices"
	"unsafe"
)

const (
	maxKeyCount = 7
	minKeyCount = 3

	nodeSize = 96
	maxDepth = 9

	maxProbes = 128
)

func keyTagSize(key string) int {
	switch {
	case len(key) < 1<<6:
		return 1
	case len(key) < 1<<14:
		return 2
	default:
		return 3
	}
}

const (
	TypeNull = iota
	TypeBool
	TypeInt
	TypeFloat
	TypeBytes
	TypeString
	TypeObject
	TypeArray
	TypeInvalid
)

var typeSizes = [...]int{
	TypeNull:    0,
	TypeBool:    1,
	TypeInt:     8,
	TypeFloat:   8,
	TypeBytes:   4,
	TypeString:  4,
	TypeObject:  nodeSize - 1, // type byte hidden inside treeNode.gen_type
	TypeArray:   nodeSize - 1,
	TypeInvalid: 0,
}

var errCollision = errors.New("hash collision")

type Buffer struct {
	buf []byte
}

func (b *Buffer) setRootContainer(typ uint8) container {
	b.buf = appendNode(b.buf[:0], &treeNode{gen_type: uint32(typ)})
	return container{
		b:   b,
		typ: typ,
		ptr: nodeptr{b, 0},
	}
}

func (b *Buffer) SetRootObject() Object { return Object{b.setRootContainer(TypeObject)} }
func (b *Buffer) SetRootArray() Array   { return Array{b.setRootContainer(TypeArray)} }

func (b *Buffer) Bytes() []byte { return b.buf }

func New(buf []byte) *Buffer {
	return &Buffer{buf: buf}
}

type Object struct {
	c container
}

func (o Object) Count() int                         { return o.c.count() }
func (o Object) Iter() *Iter                        { return o.c.iter() }
func (o Object) SetNull(key string)                 { o.c.setNull(key, keyHash(key)) }
func (o Object) SetBool(key string, val bool)       { o.c.setBool(key, keyHash(key), val) }
func (o Object) Bool(key string) bool               { return o.c.bool(key, keyHash(key)) }
func (o Object) SetInt(key string, val int)         { o.c.setInt(key, keyHash(key), val) }
func (o Object) Int(key string) int                 { return o.c.int(key, keyHash(key)) }
func (o Object) SetFloat64(key string, val float64) { o.c.setFloat64(key, keyHash(key), val) }
func (o Object) Float64(key string) float64         { return o.c.float64(key, keyHash(key)) }
func (o Object) SetBytes(key string, val []byte)    { o.c.setBytes(key, keyHash(key), val) }
func (o Object) Bytes(key string) []byte            { return o.c.bytes(key, keyHash(key)) }
func (o Object) RawBytes(key string) []byte         { return o.c.rawBytes(key, keyHash(key)) }
func (o Object) SetString(key string, val string)   { o.c.setString(key, keyHash(key), val) }
func (o Object) String(key string) string           { return o.c.string(key, keyHash(key)) }
func (o Object) RawString(key string) string        { return o.c.rawString(key, keyHash(key)) }
func (o Object) SetObject(key string) Object        { return o.c.setObject(key, keyHash(key)) }
func (o Object) Object(key string) Object           { return o.c.object(key, keyHash(key)) }
func (o Object) SetArray(key string) Array          { return o.c.setArray(key, keyHash(key)) }
func (o Object) Array(key string) Array             { return o.c.array(key, keyHash(key)) }

type Array struct {
	c container
}

func (a Array) Count() int                    { return a.c.count() }
func (a Array) Iter() *Iter                   { return a.c.iter() }
func (a Array) SetNull(i int)                 { a.c.setNull("", uint32(i)) }
func (a Array) SetBool(i int, val bool)       { a.c.setBool("", uint32(i), val) }
func (a Array) Bool(i int) bool               { return a.c.bool("", uint32(i)) }
func (a Array) SetInt(i int, val int)         { a.c.setInt("", uint32(i), val) }
func (a Array) Int(i int) int                 { return a.c.int("", uint32(i)) }
func (a Array) SetFloat64(i int, val float64) { a.c.setFloat64("", uint32(i), val) }
func (a Array) Float64(i int) float64         { return a.c.float64("", uint32(i)) }
func (a Array) SetBytes(i int, val []byte)    { a.c.setBytes("", uint32(i), val) }
func (a Array) Bytes(i int) []byte            { return a.c.bytes("", uint32(i)) }
func (a Array) RawBytes(i int) []byte         { return a.c.rawBytes("", uint32(i)) }
func (a Array) SetString(i int, val string)   { a.c.setString("", uint32(i), val) }
func (a Array) String(i int) string           { return a.c.string("", uint32(i)) }
func (a Array) RawString(i int) string        { return a.c.rawString("", uint32(i)) }
func (a Array) SetObject(i int) Object        { return a.c.setObject("", uint32(i)) }
func (a Array) Object(i int) Object           { return a.c.object("", uint32(i)) }
func (a Array) SetArray(i int) Array          { return a.c.setArray("", uint32(i)) }
func (a Array) Array(i int) Array             { return a.c.array("", uint32(i)) }

func appendNode(buf []byte, node *treeNode) []byte {
	for _, u := range (*[24]uint32)(unsafe.Pointer(node)) {
		buf = binary.LittleEndian.AppendUint32(buf, u)
	}
	return buf
}

func writeNode(buf []byte, node *treeNode) {
	appendNode(buf[:0], node)
}

func readNode(buf []byte) treeNode {
	u32s := (*[24]uint32)(unsafe.Pointer(&buf[0]))
	return treeNode{
		gen_type:   u32s[0],
		hashes:     [7]uint32(u32s[1:8]),
		size_kc:    u32s[8],
		kv_offs:    [7]uint32(u32s[9:16]),
		child_offs: [8]uint32(u32s[16:24]),
	}
}

type nodeptr struct {
	b   *Buffer
	off uint32
}

func (np nodeptr) clear() {
	for i := range uint32(nodeSize) {
		np.b.buf[np.off+i] = 0
	}
}

func (np nodeptr) get32(i uint32) uint32 {
	return binary.LittleEndian.Uint32(np.b.buf[np.off+i*4:])
}

func (np nodeptr) put32(i uint32, v uint32) {
	binary.LittleEndian.PutUint32(np.b.buf[np.off+i*4:], v)
}

func (np nodeptr) gen() uint32 {
	return np.get32(0) >> 8
}

func (np nodeptr) setGen(gen uint32) {
	genType := np.get32(0)
	genType &= (1 << 8) - 1
	genType |= gen << 8
	np.put32(0, genType)
}

func (np nodeptr) hash(i int) uint32  { return np.get32(uint32(1 + i)) }
func (np nodeptr) kv(i int) uint32    { return np.get32(uint32(9 + i)) }
func (np nodeptr) child(i int) uint32 { return np.get32(uint32(16 + i)) }

func (np nodeptr) setHash(i int, v uint32)  { np.put32(uint32(1+i), v) }
func (np nodeptr) setKV(i int, v uint32)    { np.put32(uint32(9+i), v) }
func (np nodeptr) setChild(i int, v uint32) { np.put32(uint32(16+i), v) }

func (np nodeptr) Type() uint8       { return np.b.buf[np.off] }
func (np nodeptr) setType(typ uint8) { np.b.buf[np.off] = typ }

func (np nodeptr) size() int { return int(np.get32(8) >> 6) }

func (np nodeptr) setSize(size int) {
	sizeKC := np.get32(8)
	sizeKC &= (1 << 6) - 1
	sizeKC |= uint32(size) << 6
	np.put32(8, sizeKC)
}

func (np nodeptr) keyCount() int {
	return int(np.b.buf[np.off+32] & 7)
}

func (np nodeptr) setKeyCount(kc int) {
	np.b.buf[np.off+32] &^= 7
	np.b.buf[np.off+32] |= uint8(kc) & 7
}

func (np nodeptr) read() treeNode {
	return readNode(np.b.buf[np.off:])
}

func (nptr nodeptr) write(node *treeNode) {
	writeNode(nptr.b.buf[nptr.off:], node)
}

type treeNode struct {
	gen_type   uint32
	hashes     [7]uint32
	size_kc    uint32
	kv_offs    [7]uint32
	child_offs [8]uint32
}

func (node *treeNode) Type() uint8 {
	return uint8(node.gen_type)
}

func (node *treeNode) keyCount() int {
	return int(node.size_kc & 7)
}

func (node *treeNode) setKeyCount(kc int) {
	node.size_kc &^= 7
	node.size_kc |= uint32(kc) & 7
}

func keyHash(key string) uint32 {
	// DJB2
	hash := uint32(5381)
	for i := range key {
		hash = ((hash << 5) + hash) + uint32(key[i])
	}
	return hash
}

type container struct {
	b   *Buffer
	typ uint8
	ptr nodeptr
}

func (c *container) count() int {
	return c.ptr.size()
}

func (c *container) setContainer(key string, keyHash uint32, typ uint8) container {
	off := c.b.set(c.ptr, key, keyHash, TypeObject, typeSizes[TypeObject])
	np := nodeptr{c.b, uint32(off)}
	np.clear()
	np.setType(typ)
	return container{
		b:   c.b,
		typ: typ,
		ptr: nodeptr{c.b, uint32(off)},
	}
}

func (c *container) container(key string, keyHash uint32, exp uint8) container {
	typ, off := c.b.get(c.ptr, key, keyHash)
	if typ != exp {
		panic("wrong container type")
	}
	return container{
		b:   c.b,
		typ: typ,
		ptr: nodeptr{c.b, uint32(off)},
	}
}

func (c *container) setNull(key string, keyHash uint32) {
	c.b.set(c.ptr, key, keyHash, TypeNull, 0)
}

func (c *container) setBool(key string, keyHash uint32, val bool) {
	off := c.b.set(c.ptr, key, keyHash, TypeBool, 1)
	if val {
		c.b.buf[off] = 1
	} else {
		c.b.buf[off] = 0
	}
}

func (c *container) bool(key string, keyHash uint32) bool {
	typ, off := c.b.get(c.ptr, key, keyHash)
	buf := c.b.buf[off:]
	return typ == TypeBool && len(buf) > 0 && buf[0] != 0
}

func (c *container) setUint64(key string, keyHash uint32, typ uint8, val uint64) {
	buf := c.b.buf[c.b.set(c.ptr, key, keyHash, typ, 8):]
	binary.LittleEndian.PutUint64(buf, val)
}

func (c *container) number(key string, keyHash uint32, exp uint8) uint64 {
	typ, off := c.b.get(c.ptr, key, keyHash)
	buf := c.b.buf[off:]
	if len(buf) >= 8 {
		return binary.LittleEndian.Uint64(buf)
	} else if typ != exp {
		panic("wrong type")
	}
	return 0
}

func (c *container) setInt(key string, keyHash uint32, val int) {
	c.setUint64(key, keyHash, TypeInt, uint64(val))
}

func (c *container) int(key string, keyHash uint32) int {
	return int(c.number(key, keyHash, TypeInt))
}

func (c *container) setFloat64(key string, keyHash uint32, val float64) {
	c.setUint64(key, keyHash, TypeFloat, math.Float64bits(val))
}

func (c *container) float64(key string, keyHash uint32) float64 {
	return math.Float64frombits(c.number(key, keyHash, TypeFloat))
}

func (c *container) setTypedBytes(key string, keyHash uint32, typ uint8, val []byte) {
	buf := c.b.buf[c.b.set(c.ptr, key, keyHash, typ, 4+len(val)+1):]
	binary.LittleEndian.PutUint32(buf, uint32(len(val)+1))
	copy(buf[4:], val)
	buf[4+len(val)] = 0 // C string terminator
}

func (c *container) typedBytes(key string, keyHash uint32, exp uint8) []byte {
	typ, off := c.b.get(c.ptr, key, keyHash)
	buf := c.b.buf[off:]
	if len(buf) < 4 {
		return nil
	} else if typ != exp {
		return nil
	}
	strlen := binary.LittleEndian.Uint32(buf[:4]) - 1 // C string terminator
	if len(buf[4:]) < int(strlen) {
		return nil
	}
	return buf[4:][:strlen]
}

func (c *container) setBytes(key string, keyHash uint32, val []byte) {
	c.setTypedBytes(key, keyHash, TypeBytes, val)
}

func (c *container) bytes(key string, keyHash uint32) []byte {
	return slices.Clone(c.rawBytes(key, keyHash))
}

func (c *container) rawBytes(key string, keyHash uint32) []byte {
	return c.typedBytes(key, keyHash, TypeBytes)
}

func (c *container) setString(key string, keyHash uint32, val string) {
	c.setTypedBytes(key, keyHash, TypeString, unsafe.Slice(unsafe.StringData(val), len(val)))
}

func (c *container) string(key string, keyHash uint32) string {
	return string(c.typedBytes(key, keyHash, TypeString))
}

func (c *container) rawString(key string, keyHash uint32) string {
	b := c.typedBytes(key, keyHash, TypeString)
	return unsafe.String(&b[0], len(b))
}

func (c *container) setObject(key string, keyHash uint32) Object {
	return Object{c.setContainer(key, keyHash, TypeObject)}
}

func (c *container) object(key string, keyHash uint32) Object {
	return Object{c.container(key, keyHash, TypeObject)}
}

func (c *container) setArray(key string, keyHash uint32) Array {
	return Array{c.setContainer(key, keyHash, TypeArray)}
}

func (c *container) array(key string, keyHash uint32) Array {
	return Array{c.container(key, keyHash, TypeArray)}
}

func (c *container) iter() *Iter {
	node := c.ptr
	it := &Iter{
		b:   c.b,
		gen: node.gen(),
	}
	it.node_offs[0] = uint32(c.ptr.off)

	for node.child(0) != 0 { // has children, travel down
		node.off = node.child(0)
		if it.depth++; it.depth > maxDepth {
			return &Iter{err: errors.New("node walks exceeded maxDepth")}
		}
		it.node_offs[it.depth] = node.off
		it.node_i[it.depth] = 0
	}
	return it
}

// Verifies a key inside the buffer to ensure readers don't go out of bounds.
// Optionally, compares the existing key to an input key; a mismatch implies a
// hash collision.
func _verify_key(buf []byte, key string, key_tag_size int, offs *int) (out_key_tag_size int, err error) {
	const maxTagSize = 4
	if len(buf) < maxTagSize {
		return 0, errors.New("key entry out of bounds")
	}
	_key_tag_size := int((buf[*offs] & (maxTagSize - 1)) + 1)
	if key_tag_size != 0 && key_tag_size != _key_tag_size {
		return 0, errors.New("key tag size does not match")
	}
	_key_size := (binary.LittleEndian.Uint32(buf[*offs:]) & ((1 << 8 * uint32(_key_tag_size)) - 1)) >> 2
	*offs += _key_tag_size
	if len(buf) < int(_key_size)+*offs {
		return 0, errors.New("key entry out of bounds")
	}
	if key != "" && key != string(buf[*offs:][:_key_size-1]) {
		return 0, errCollision
	}
	*offs += int(_key_size)
	if out_key_tag_size != 0 {
		out_key_tag_size = _key_tag_size
	}
	return out_key_tag_size, nil
}

func _verify_val(buf []byte, off int) (int, error) {
	if len(buf) < 1 || off > len(buf)-1 {
		return 0, errors.New("value out of bounds")
	}
	typ := buf[off]
	if typ == TypeInvalid {
		return 0, errors.New("value type invalid")
	}
	_val_entry_size := 1 + typeSizes[typ]

	if _val_entry_size > len(buf) || off > len(buf)-_val_entry_size {
		return 0, errors.New("value out of bounds")
	}
	if typ == TypeString || typ == TypeBytes { // extra check required for str/bytes
		byte_count := binary.LittleEndian.Uint32(buf[off+1:])
		_val_entry_size += int(byte_count)
		if _val_entry_size > len(buf) || off > len(buf)-_val_entry_size {
			return 0, errors.New("value out of bounds")
		}
	}
	return off + _val_entry_size, nil
}

func (b *Buffer) set(root nodeptr, key string, keyHash uint32, val_typ uint8, val_len int) int {
	key_tag_size := keyTagSize(key)
	base_entry_size := key_tag_size + len(key) + 1 + val_len

	root.setGen(root.gen() + 1)

	probe_attempts := uint32(1)
	if key != "" {
		probe_attempts = maxProbes
	}

outer:
	for attempt := uint32(0); ; attempt++ {
		attempt_key_hash := keyHash + attempt*attempt

		entry_size := base_entry_size
		var parent treeNode
		var parentptr nodeptr
		parentptr.b = b
		node := root.read()
		nptr := root

		var key_count, i int
		node_walks := 0
		for {
			match := false
			if nptr.keyCount() == maxKeyCount { // node full, need to split
				buflen_aligned := (len(b.buf) + 3) & ^3 // next multiple of 4
				new_node_size := nodeSize
				if parent.gen_type != 0 {
					new_node_size = 2 * nodeSize
				}
				if len(b.buf)+new_node_size+buflen_aligned > cap(b.buf) {
					// grow buffer
					b.buf = append(b.buf, make([]byte, new_node_size+buflen_aligned)...)
				}
				b.buf = b.buf[:buflen_aligned]
				if parent.gen_type == 0 {
					// if root split, create new root
					nptr.off = uint32(len(b.buf))
					parentptr = root
					parent = parentptr.read()
					parent.setKeyCount(0)
					parent.hashes = [7]uint32{}
					parent.kv_offs = [7]uint32{}
					parent.child_offs = [8]uint32{0: uint32(len(b.buf))}
					b.buf = b.buf[:len(b.buf)+nodeSize]
					key_count = 0
					i = 0
				}
				for j := key_count; j > i; j-- { // shift parent array before separator insert
					parent.hashes[j] = parent.hashes[j-1]
					parent.kv_offs[j] = parent.kv_offs[j-1]
					parent.child_offs[j+1] = parent.child_offs[j]
				}
				parent.hashes[i] = node.hashes[minKeyCount] // insert new separator key in parent
				parent.kv_offs[i] = node.kv_offs[minKeyCount]
				parent.child_offs[i+1] = uint32(len(b.buf)) // insert sibling as child in parent
				parent.setKeyCount(parent.keyCount() + 1)
				parentptr.write(&parent)

				node.hashes[minKeyCount] = 0
				node.kv_offs[minKeyCount] = 0
				sibling := treeNode{
					gen_type: uint32(node.Type()),
					size_kc:  minKeyCount,
				}
				node.size_kc = minKeyCount
				sibling.child_offs[0] = node.child_offs[minKeyCount+1] // take child from node
				node.child_offs[minKeyCount+1] = 0
				for j := range minKeyCount { // copy half of node's keys to sibling
					sibling.hashes[j] = node.hashes[j+minKeyCount+1]
					sibling.kv_offs[j] = node.kv_offs[j+minKeyCount+1]
					sibling.child_offs[j+1] = node.child_offs[j+minKeyCount+2]
					node.hashes[j+minKeyCount+1] = 0
					node.kv_offs[j+minKeyCount+1] = 0
					node.child_offs[j+minKeyCount+2] = 0
				}
				nptr.write(&node)
				b.buf = appendNode(b.buf, &sibling)

				if attempt_key_hash > parent.hashes[i] { // sibling has target key? then we follow
					//node = sibling
					node = sibling
					nptr.off = uint32(len(b.buf) - nodeSize)
				} else if attempt_key_hash == parent.hashes[i] {
					// node = parent
					node = parent
					nptr.off = parentptr.off
					match = true
				}
			}

			if !match {
				key_count = nptr.keyCount()
				i = 0
				for i < key_count && node.hashes[i] < attempt_key_hash {
					i++
				}
				match = i < key_count && node.hashes[i] == attempt_key_hash
			}
			if match {
				target_offs := int(node.kv_offs[i])
				key_start_offs := target_offs
				if key != "" {
					if _, err := _verify_key(b.buf, key, key_tag_size, &target_offs); err == errCollision {
						break // try next probe
					} else if err != nil {
						panic(err)
					}
				}
				val_start_offs := target_offs
				verify, err := _verify_val(b.buf, target_offs)
				if err != nil {
					panic(err)
				}
				if val_len >= verify-val_start_offs { // value is too large, we must append
					alignment_mask := 0
					if val_typ == TypeObject || val_typ == TypeArray {
						alignment_mask = 3
					}
					unaligned_val_offs := len(b.buf) + key_tag_size + len(key)
					alignment_padding := ((unaligned_val_offs + alignment_mask) & ^alignment_mask) - unaligned_val_offs
					entry_size += alignment_padding
					for j := 0; j < target_offs-key_start_offs; j++ {
						b.buf[int(node.kv_offs[i])+j] = 0
					}
					b.buf = b.buf[:len(b.buf)+alignment_padding]
					node.kv_offs[i] = uint32(len(b.buf))
					nptr.write(&node)
					break outer
				}
				for i := 0; i < target_offs-val_start_offs; i++ {
					b.buf[val_start_offs+i] = 0
				}

				if val_typ == TypeObject || val_typ == TypeArray {
					return val_start_offs
				}
				b.buf[val_start_offs] = val_typ
				return val_start_offs + 1
			}

			if node.child_offs[0] != 0 {
				parentptr.off = uint32(nptr.off)
				parent = parentptr.read()
				nptr.off = nptr.child(i)
				node = nptr.read()
				if node_walks++; node_walks > maxDepth {
					panic(errors.New("node walks exceeded maxDepth"))
				}
			} else {
				// insert the kv-pair
				alignment_mask := 0
				if val_typ == TypeObject || val_typ == TypeArray {
					alignment_mask = 3
				}
				unaligned_val_offs := len(b.buf) + key_tag_size + len(key)
				alignment_padding := ((unaligned_val_offs + alignment_mask) & ^alignment_mask) - unaligned_val_offs
				entry_size += alignment_padding
				for j := key_count; j > i; j-- {
					node.hashes[j] = node.hashes[j-1]
					node.kv_offs[j] = node.kv_offs[j-1]
				}
				node.hashes[i] = attempt_key_hash
				node.setKeyCount(nptr.keyCount() + 1)
				b.buf = b.buf[:len(b.buf)+alignment_padding]
				node.kv_offs[i] = uint32(len(b.buf))
				nptr.write(&node)
				root.setSize(root.size() + 1)
				break outer
			}
		}

		if attempt++; attempt == probe_attempts {
			panic(errors.New("maxProbes limit reached"))
		}
	}

	if key != "" {
		b.buf = binary.LittleEndian.AppendUint32(b.buf, uint32(((len(key)+1)<<2)|(key_tag_size-1)))
		b.buf = b.buf[:len(b.buf)-4+key_tag_size]
		b.buf = append(b.buf, []byte(key)...) // TODO: copy instead
		b.buf = append(b.buf, 0)              // C string terminator
	}
	b.buf = append(b.buf, val_typ)
	b.buf = append(b.buf, make([]byte, val_len)...)
	if val_typ == TypeObject || val_typ == TypeArray {
		return len(b.buf) - val_len - 1
	}
	return len(b.buf) - val_len
}

func (b *Buffer) get(root nodeptr, key string, keyHash uint32) (uint8, int) {
	key_tag_size := keyTagSize(key)
	probe_attempts := uint32(1)
	if key != "" {
		probe_attempts = maxProbes
	}

	for attempt := uint32(0); attempt < probe_attempts; attempt++ {
		attempt_key_hash := keyHash + attempt*attempt

		node := root
		node_walks := 0
		for {
			key_count := node.keyCount()
			i := 0
			for i < key_count && node.hash(i) < attempt_key_hash {
				i++
			}
			if i < key_count && node.hash(i) == attempt_key_hash { // target key found
				target_offs := int(node.kv(i))
				if key != "" {
					if _, err := _verify_key(b.buf, key, key_tag_size, &target_offs); err == errCollision {
						break // try next probe
					} else if err != nil {
						panic(err)
					}
				}
				val_start_offs := target_offs
				if _, err := _verify_val(b.buf, target_offs); err != nil {
					panic(err)
				}
				typ := b.buf[val_start_offs]
				if typ == TypeObject || typ == TypeArray {
					return typ, val_start_offs
				}
				return typ, val_start_offs + 1
			}
			if node.child(0) == 0 {
				panic(errors.New("key not found: " + key))
			}
			node.off = node.child(i)

			if node_walks++; node_walks > maxDepth {
				panic(errors.New("node walks exceeded maxDepth"))
			}
		}
	}
	panic(errors.New("maxProbes limit reached"))
}

type Value struct {
	Type uint8
	data []byte
	c    container
}

func (v Value) Any() any {
	switch v.Type {
	case TypeNull:
		return nil
	case TypeBool:
		return v.Bool()
	case TypeInt:
		return v.Int()
	case TypeFloat:
		return v.Float64()
	case TypeBytes:
		return v.Bytes()
	case TypeString:
		return v.String()
	case TypeObject:
		return v.Object()
	case TypeArray:
		return v.Array()
	default:
		panic("unhandled type")
	}
}

func (v Value) Bool() bool       { return v.data[0] != 0 }
func (v Value) Uint64() uint64   { return binary.LittleEndian.Uint64(v.data) }
func (v Value) Int() int         { return int(v.Uint64()) }
func (v Value) Float64() float64 { return math.Float64frombits(v.Uint64()) }
func (v Value) RawBytes() []byte { return v.data[4:][:binary.LittleEndian.Uint32(v.data[:4])-1] }
func (v Value) Bytes() []byte    { return slices.Clone(v.RawBytes()) }
func (v Value) String() string   { return string(v.RawBytes()) }
func (v Value) RawString() string {
	return unsafe.String(&v.data[4], int(binary.LittleEndian.Uint32(v.data[:4])))
}
func (v Value) Object() Object { return Object{c: v.c} }
func (v Value) Array() Array   { return Array{c: v.c} }

type IterElem struct {
	Key   string
	Index int
	Value Value
}

type Iter struct {
	b         *Buffer
	gen       uint32
	node_offs [maxDepth + 1]uint32
	depth     int
	node_i    [maxDepth + 1]int
	err       error
}

func (it *Iter) Err() error {
	return it.err
}

func (it *Iter) Next() *IterElem {
	if it.err != nil {
		return nil
	} else if it.gen != (nodeptr{it.b, it.node_offs[0]}).gen() {
		return nil
	}
	node := nodeptr{it.b, it.node_offs[it.depth]}
	nodeTyp := node.Type()
	if !(nodeTyp == TypeObject || nodeTyp == TypeArray) {
		it.err = errors.New("not an array or object type")
		return nil
	}
	if it.depth == 0 && it.node_i[it.depth] == node.keyCount() {
		return nil
	}

	var key string
	var index int
	target_offs := int(node.kv(it.node_i[it.depth]))
	if nodeTyp == TypeObject { // write back key if not NULL
		key_start_offs := target_offs
		if _, err := _verify_key(it.b.buf, "", 0, &target_offs); err != nil {
			it.err = err
			return nil
		}
		key = string(it.b.buf[key_start_offs+1:][:(target_offs - key_start_offs - 2)]) // exclude C string terminator
	} else {
		index = int(node.hash(it.node_i[it.depth]))
	}
	val_start_offs := target_offs
	if _, err := _verify_val(it.b.buf, target_offs); err != nil {
		it.err = err
		return nil
	}
	typ, val := it.b.buf[val_start_offs], it.b.buf[val_start_offs+1:]
	var c container
	if typ == TypeObject || typ == TypeArray {
		c = container{it.b, typ, nodeptr{it.b, uint32(val_start_offs)}}
	}
	it.node_i[it.depth]++

	for node.child(it.node_i[it.depth]) != 0 { // has children, travel down
		next_node_offs := node.child(it.node_i[it.depth])
		node = nodeptr{it.b, next_node_offs}
		if it.depth++; it.depth > maxDepth {
			it.err = errors.New("node walks exceeded maxDepth")
			return nil
		}
		it.node_offs[it.depth] = next_node_offs
		it.node_i[it.depth] = 0
	}
	for it.depth > 0 && it.node_i[it.depth] == node.keyCount() {
		it.depth--
		node = nodeptr{it.b, it.node_offs[it.depth]}
	}
	return &IterElem{key, index, Value{Type: typ, data: val, c: c}}
}
