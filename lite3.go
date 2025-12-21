// Package lite3 is an implementation of the Lite³ serialization format.
package lite3

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"unsafe"
)

const (
	LITE3_NODE_TYPE_SHIFT = 0
	LITE3_NODE_TYPE_MASK  = ((1 << 8) - 1) // 8 LSB

	LITE3_NODE_GEN_SHIFT = 8
	LITE3_NODE_GEN_MASK  = (^((1 << 8) - 1)) // 24 MSB

	LITE3_NODE_KEY_COUNT_MAX = 7
	LITE3_NODE_KEY_COUNT_MIN = (LITE3_NODE_KEY_COUNT_MAX / 2)

	LITE3_NODE_KEY_COUNT_SHIFT = 0
	LITE3_NODE_KEY_COUNT_MASK  = ((1 << 3) - 1) // 3 LSB	key_count: 0-7          hashes[7]       kv_offs[7]       child_offs[8]	LITE3_NODE_SIZE: 96 (1.5 cache lines)

	LITE3_KEY_TAG_SIZE_MIN   = 1
	LITE3_KEY_TAG_SIZE_MAX   = 4
	LITE3_KEY_TAG_SIZE_MASK  = ((1 << 2) - 1)
	LITE3_KEY_TAG_SIZE_SHIFT = 0

	LITE3_KEY_TAG_KEY_SIZE_MASK  = (^((1 << 2) - 1))
	LITE3_KEY_TAG_KEY_SIZE_SHIFT = 2

	LITE3_NODE_SIZE           = 96
	LITE3_TREE_HEIGHT_MAX     = 9 // key_count: 0-7       LITE3_NODE_SIZE: 96 (1.5 cache lines)
	LITE3_NODE_SIZE_KC_OFFSET = 32
	LITE3_NODE_SIZE_SHIFT     = 6
	LITE3_NODE_SIZE_MASK      = (^((1 << 6) - 1)) // 26 MSB

	LITE3_HASH_PROBE_MAX = 128

	LITE3_VAL_SIZE = 1

	LITE3_NODE_ALIGNMENT      = 4
	LITE3_NODE_ALIGNMENT_MASK = (LITE3_NODE_ALIGNMENT - 1)
)

const (
	LITE3_TYPE_NULL    = iota //  maps to 'null' type in JSON
	LITE3_TYPE_BOOL           //  maps to 'boolean' type in JSON; underlying datatype: `bool`
	LITE3_TYPE_I64            //  maps to 'number' type in JSON; underlying datatype: `int64`
	LITE3_TYPE_F64            //  maps to 'number' type in JSON; underlying datatype: `float64`
	LITE3_TYPE_BYTES          //  coverted to base64 string in JSON
	LITE3_TYPE_STRING         //  maps to 'string' type in JSON
	LITE3_TYPE_OBJECT         //  maps to 'object' type in JSON
	LITE3_TYPE_ARRAY          //  maps to 'array' type in JSON
	LITE3_TYPE_INVALID        //  any type value equal or greater than this is considered invalid
	LITE3_TYPE_COUNT          //  not an actual type, only used for counting
)

var lite3_type_sizes = [...]int{
	LITE3_TYPE_NULL:    0,
	LITE3_TYPE_BOOL:    1,
	LITE3_TYPE_I64:     8,
	LITE3_TYPE_F64:     8,
	LITE3_TYPE_BYTES:   4,
	LITE3_TYPE_STRING:  4,
	LITE3_TYPE_OBJECT:  LITE3_NODE_SIZE - LITE3_VAL_SIZE, // `type` field is contained inside node->gen_type
	LITE3_TYPE_ARRAY:   LITE3_NODE_SIZE - LITE3_VAL_SIZE, // `type` field is contained inside node->gen_type
	LITE3_TYPE_INVALID: 0,
}

var errCollision = errors.New("key hash collision")

type Cursor struct {
	buf  []byte
	off  int
	node *treeNode

	key     string
	keyHash uint32
}

func (c *Cursor) Reset() {
	c.off = 0
	c.node = cast[treeNode](c.buf, c.off)
	c.key = ""
	c.keyHash = 0
}

func (c *Cursor) Type() uint8 {
	return c.buf[c.off] & LITE3_NODE_TYPE_MASK
}

func (c *Cursor) Count() int {
	return int(c.node.size_kc >> LITE3_NODE_SIZE_SHIFT)
}

func (c *Cursor) Field(key string) {
	c.key = key
	c.keyHash = keyHash(key)
}

func (c *Cursor) Index(i int) {
	c.key = ""
	c.keyHash = uint32(i)
}

func (c *Cursor) Append() {
	c.Index(c.Count())
}

func (c *Cursor) NewObject() {
	c.setContainer(LITE3_TYPE_OBJECT)
}

func (c *Cursor) NewArray() {
	c.setContainer(LITE3_TYPE_ARRAY)
}

func (c *Cursor) SetNull() {
	c.set(LITE3_TYPE_NULL, 0)
}

func (c *Cursor) SetBool(val bool) {
	var buf [1]byte
	if val {
		buf[0] = 1
	}
	copy(c.set(LITE3_TYPE_BOOL, 1), buf[:])
}

func (c *Cursor) Bool() bool {
	buf := c.get(LITE3_TYPE_BOOL)
	return len(buf) > 0 && buf[0] != 0
}

func (c *Cursor) setUint64(typ uint8, val uint64) {
	buf := c.set(typ, 8)
	if len(buf) >= 8 {
		binary.LittleEndian.PutUint64(buf, val)
	}
}

func (c *Cursor) getUint64(typ uint8) uint64 {
	buf := c.get(typ)
	if len(buf) >= 8 {
		return binary.LittleEndian.Uint64(buf)
	}
	return 0
}

func (c *Cursor) SetInt(val int) {
	c.setUint64(LITE3_TYPE_I64, uint64(val))
}

func (c *Cursor) Int() int {
	return int(c.getUint64(LITE3_TYPE_I64))
}

func (c *Cursor) SetFloat64(val float64) {
	c.setUint64(LITE3_TYPE_F64, math.Float64bits(val))
}

func (c *Cursor) Float64() float64 {
	return math.Float64frombits(c.getUint64(LITE3_TYPE_F64))
}

func (c *Cursor) setBytes(typ uint8, val []byte) {
	buf := c.set(typ, 4+len(val)+1)
	binary.LittleEndian.PutUint32(buf, uint32(len(val)+1))
	copy(buf[4:], val)
	buf[4+len(val)] = 0 // C string terminator
}

func (c *Cursor) getBytes(typ uint8) []byte {
	buf := c.get(typ)
	if len(buf) < 4 {
		return nil
	}
	strlen := binary.LittleEndian.Uint32(buf[:4]) - 1 // C string terminator
	if len(buf[4:]) < int(strlen) {
		return nil
	}
	return buf[4:][:strlen]
}

func (c *Cursor) SetBytes(val []byte) {
	c.setBytes(LITE3_TYPE_BYTES, val)
}

func (c *Cursor) Bytes() []byte {
	return slices.Clone(c.RawBytes())
}

func (c *Cursor) RawBytes() []byte {
	return c.getBytes(LITE3_TYPE_BYTES)
}

func (c *Cursor) SetString(val string) {
	c.setBytes(LITE3_TYPE_STRING, unsafe.Slice(unsafe.StringData(val), len(val)))
}

func (c *Cursor) String() string {
	// TODO: handle all types
	return string(c.getBytes(LITE3_TYPE_STRING))
}

func (c *Cursor) RawString() string {
	b := c.getBytes(LITE3_TYPE_STRING)
	return unsafe.String(&b[0], len(b))
}

func (c *Cursor) Buffer() []byte {
	return c.buf
}

func New(buf []byte) *Cursor {
	return &Cursor{buf: buf}
}

////

func cast[T any](buf []byte, off int) *T {
	return (*T)(unsafe.Pointer(uintptr(unsafe.Pointer(unsafe.SliceData(buf))) + uintptr(off)))
}

type treeNode struct {
	gen_type   uint32
	hashes     [7]uint32
	size_kc    uint32
	kv_offs    [7]uint32
	child_offs [8]uint32
}

func keyHash(key string) uint32 {
	hash := uint32(5381) // LITE3_DJB2_HASH_SEED
	for i := range key {
		hash = ((hash << 5) + hash) + uint32(key[i])
	}
	return hash
}

func (c *Cursor) setContainer(typ uint8) {
	c.set(LITE3_TYPE_OBJECT, lite3_type_sizes[LITE3_TYPE_OBJECT])
	c.node = cast[treeNode](c.buf, c.off)
	*c.node = treeNode{gen_type: uint32(typ & LITE3_NODE_TYPE_MASK)}
}

// Verifies a key inside the buffer to ensure readers don't go out of bounds.
// Optionally, compares the existing key to an input key; a mismatch implies a
// hash collision.
func _verify_key(buf []byte, key string, key_tag_size int, offs *int) (out_key_tag_size int, err error) {
	if len(buf) < LITE3_KEY_TAG_SIZE_MAX {
		return 0, errors.New("key entry out of bounds")
	}
	_key_tag_size := int((buf[*offs] & LITE3_KEY_TAG_SIZE_MASK) + 1)
	if key_tag_size != 0 && key_tag_size != _key_tag_size {
		return 0, errors.New("key tag size does not match")
	}
	_key_size := (*cast[uint32](buf, *offs) & ((1 << 8 * uint32(_key_tag_size)) - 1)) >> LITE3_KEY_TAG_KEY_SIZE_SHIFT
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
	if len(buf) < LITE3_VAL_SIZE || off > len(buf)-LITE3_VAL_SIZE {
		return 0, errors.New("value out of bounds")
	}
	typ := *cast[uint8](buf, off)
	if typ == LITE3_TYPE_INVALID {
		return 0, errors.New("value type invalid")
	}
	_val_entry_size := LITE3_VAL_SIZE + lite3_type_sizes[typ]

	if _val_entry_size > len(buf) || off > len(buf)-_val_entry_size {
		return 0, errors.New("value out of bounds")
	}
	if typ == LITE3_TYPE_STRING || typ == LITE3_TYPE_BYTES { // extra check required for str/bytes
		byte_count := *cast[uint32](buf, off+LITE3_VAL_SIZE)
		_val_entry_size += int(byte_count)
		if _val_entry_size > len(buf) || off > len(buf)-_val_entry_size {
			return 0, errors.New("value out of bounds")
		}
	}
	return off + _val_entry_size, nil
}

func (c *Cursor) set(val_typ uint8, val_len int) []byte {
	off := c.off
	key := c.key
	keyHash := c.keyHash

	key_tag_size := 0
	if ((len(key) >> (16 - LITE3_KEY_TAG_KEY_SIZE_SHIFT)) << 1) > 0 {
		key_tag_size++
	}
	if (len(key) >> (8 - LITE3_KEY_TAG_KEY_SIZE_SHIFT)) > 0 {
		key_tag_size++
	}
	if len(key) > 0 {
		key_tag_size++
	}
	base_entry_size := key_tag_size + len(key) + LITE3_VAL_SIZE + val_len

	root := cast[treeNode](c.buf, off)
	gen := (root.gen_type >> LITE3_NODE_GEN_SHIFT) + 1
	root.gen_type = (root.gen_type & ^LITE3_NODE_GEN_MASK) | (gen << LITE3_NODE_GEN_SHIFT)

	probe_attempts := uint32(1)
	if key != "" {
		probe_attempts = LITE3_HASH_PROBE_MAX
	}

outer:
	for attempt := uint32(0); ; attempt++ {
		attempt_key_hash := keyHash + attempt*attempt

		entry_size := base_entry_size
		var parent *treeNode
		node := root

		var key_count, i int
		node_walks := 0
		for {
			match := false
			if node.size_kc&LITE3_NODE_KEY_COUNT_MASK == LITE3_NODE_KEY_COUNT_MAX { // node full, need to split
				buflen_aligned := (len(c.buf) + LITE3_NODE_ALIGNMENT_MASK) & ^LITE3_NODE_ALIGNMENT_MASK // next multiple of LITE3_NODE_ALIGNMENT
				new_node_size := LITE3_NODE_SIZE
				if parent != nil {
					new_node_size = 2 * LITE3_NODE_SIZE
				}
				if new_node_size > cap(c.buf)-len(c.buf) || buflen_aligned > cap(c.buf)-len(c.buf)-new_node_size {
					// TODO: grow buffer instead
					panic(errors.New("no buffer space for node split"))
				}
				c.buf = c.buf[:buflen_aligned]
				if parent == nil {
					// if root split, create new root
					*cast[treeNode](c.buf, buflen_aligned) = *node
					node = cast[treeNode](c.buf, buflen_aligned)
					parent = cast[treeNode](c.buf, off)
					*parent = treeNode{
						gen_type: parent.gen_type,
						size_kc:  parent.size_kc &^ LITE3_NODE_KEY_COUNT_MASK,
					}
					parent.child_offs[0] = uint32(len(c.buf))
					c.buf = c.buf[:len(c.buf)+LITE3_NODE_SIZE]
					key_count = 0
					i = 0
				}
				for j := key_count; j > i; j-- { // shift parent array before separator insert
					parent.hashes[j] = parent.hashes[j-1]
					parent.kv_offs[j] = parent.kv_offs[j-1]
					parent.child_offs[j+1] = parent.child_offs[j]
				}
				parent.hashes[i] = node.hashes[LITE3_NODE_KEY_COUNT_MIN] // insert new separator key in parent
				parent.kv_offs[i] = node.kv_offs[LITE3_NODE_KEY_COUNT_MIN]
				parent.child_offs[i+1] = uint32(len(c.buf))                                                                                 // insert sibling as child in parent
				parent.size_kc = (parent.size_kc & ^uint32(LITE3_NODE_KEY_COUNT_MASK)) | ((parent.size_kc + 1) & LITE3_NODE_KEY_COUNT_MASK) // key_count++

				node.hashes[LITE3_NODE_KEY_COUNT_MIN] = 0
				node.kv_offs[LITE3_NODE_KEY_COUNT_MIN] = 0
				sibling := cast[treeNode](c.buf, len(c.buf))
				*sibling = treeNode{
					gen_type: node.gen_type & LITE3_NODE_TYPE_MASK,
					size_kc:  LITE3_NODE_KEY_COUNT_MIN & LITE3_NODE_KEY_COUNT_MASK,
				}
				node.size_kc = LITE3_NODE_KEY_COUNT_MIN & LITE3_NODE_KEY_COUNT_MASK
				sibling.child_offs[0] = node.child_offs[LITE3_NODE_KEY_COUNT_MIN+1] // take child from node
				node.child_offs[LITE3_NODE_KEY_COUNT_MIN+1] = 0
				for j := 0; j < LITE3_NODE_KEY_COUNT_MIN; j++ { // copy half of node's keys to sibling
					sibling.hashes[j] = node.hashes[j+LITE3_NODE_KEY_COUNT_MIN+1]
					sibling.kv_offs[j] = node.kv_offs[j+LITE3_NODE_KEY_COUNT_MIN+1]
					sibling.child_offs[j+1] = node.child_offs[j+LITE3_NODE_KEY_COUNT_MIN+2]
					node.hashes[j+LITE3_NODE_KEY_COUNT_MIN+1] = 0
					node.kv_offs[j+LITE3_NODE_KEY_COUNT_MIN+1] = 0
					node.child_offs[j+LITE3_NODE_KEY_COUNT_MIN+2] = 0
				}
				c.buf = c.buf[:len(c.buf)+LITE3_NODE_SIZE]
				if attempt_key_hash > parent.hashes[i] { // sibling has target key? then we follow
					node = sibling
				} else if attempt_key_hash == parent.hashes[i] {
					node = parent
					match = true
				}
			}

			if !match {
				key_count = int(node.size_kc & LITE3_NODE_KEY_COUNT_MASK)
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
					if _, err := _verify_key(c.buf, key, key_tag_size, &target_offs); err == errCollision {
						break // try next probe
					} else if err != nil {
						panic(err)
					}
				}
				val_start_offs := target_offs
				verify, err := _verify_val(c.buf, target_offs)
				if err != nil {
					panic(err)
				}
				if val_len >= verify-val_start_offs { // value is too large, we must append
					alignment_mask := 0
					unaligned_val_offs := len(c.buf) + key_tag_size + len(key)
					alignment_padding := ((unaligned_val_offs + alignment_mask) & ^alignment_mask) - unaligned_val_offs
					entry_size += alignment_padding
					if entry_size > cap(c.buf)-len(c.buf) {
						// TODO: grow buffer instead
						panic(errors.New("no buffer space for entry insertion"))
					}
					for j := 0; j < target_offs-key_start_offs; j++ {
						c.buf[int(node.kv_offs[i])+j] = 0
					}
					c.buf = c.buf[:len(c.buf)+alignment_padding]
					node.kv_offs[i] = uint32(len(c.buf))
					break outer
				}
				for i := 0; i < target_offs-val_start_offs; i++ {
					c.buf[val_start_offs+i] = 0
				}
				return c.buf[val_start_offs:]
			}

			if node.child_offs[0] != 0 {
				next_node_offs := int(node.child_offs[i])
				parent = node
				node = cast[treeNode](c.buf, next_node_offs)
				if node_walks++; node_walks > LITE3_TREE_HEIGHT_MAX {
					panic(errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX"))
				}
			} else {
				// insert the kv-pair
				alignment_mask := 0
				unaligned_val_offs := len(c.buf) + key_tag_size + len(key)
				alignment_padding := ((unaligned_val_offs + alignment_mask) & ^alignment_mask) - unaligned_val_offs
				entry_size += alignment_padding
				if entry_size > cap(c.buf)-len(c.buf) {
					// TODO: grow buffer instead
					panic(errors.New("no buffer space for entry insertion"))
				}
				for j := key_count; j > i; j-- {
					node.hashes[j] = node.hashes[j-1]
					node.kv_offs[j] = node.kv_offs[j-1]
				}
				node.hashes[i] = attempt_key_hash
				node.size_kc = (node.size_kc & ^uint32(LITE3_NODE_KEY_COUNT_MASK)) | ((node.size_kc + 1) & LITE3_NODE_KEY_COUNT_MASK) // key_count++
				c.buf = c.buf[:len(c.buf)+alignment_padding]
				node.kv_offs[i] = uint32(len(c.buf))
				root = cast[treeNode](c.buf, off)
				size := (root.size_kc >> LITE3_NODE_SIZE_SHIFT) + 1
				root.size_kc = (root.size_kc & ^LITE3_NODE_SIZE_MASK) | (size << LITE3_NODE_SIZE_SHIFT) // node size++
				break outer
			}
		}

		if attempt++; attempt == probe_attempts {
			panic(errors.New("LITE3_HASH_PROBE_MAX limit reached"))
		}
	}

	if key != "" {
		key_size_tmp := ((len(key) + 1) << LITE3_KEY_TAG_KEY_SIZE_SHIFT) | (key_tag_size - 1)
		c.buf = binary.LittleEndian.AppendUint32(c.buf, uint32(key_size_tmp))
		c.buf = c.buf[:len(c.buf)-4+key_tag_size]
		c.buf = append(c.buf, []byte(key)...) // TODO: copy instead
		c.buf = append(c.buf, 0)              // C string terminator
	}
	c.buf = append(c.buf, val_typ)
	c.buf = c.buf[:len(c.buf)+val_len]
	return c.buf[len(c.buf)-val_len:]
}

func (c *Cursor) get(val_typ uint8) []byte {
	off := c.off
	key := c.key
	keyHash := c.keyHash

	key_tag_size := 0
	if ((len(key) >> (16 - LITE3_KEY_TAG_KEY_SIZE_SHIFT)) << 1) > 0 {
		key_tag_size++
	}
	if (len(key) >> (8 - LITE3_KEY_TAG_KEY_SIZE_SHIFT)) > 0 {
		key_tag_size++
	}
	if len(key) > 0 {
		key_tag_size++
	}

	probe_attempts := uint32(1)
	if key != "" {
		probe_attempts = LITE3_HASH_PROBE_MAX
	}

	for attempt := uint32(0); attempt < probe_attempts; attempt++ {
		attempt_key_hash := keyHash + attempt*attempt

		node := cast[treeNode](c.buf, off)

		node_walks := 0
		for {
			key_count := int(node.size_kc & LITE3_NODE_KEY_COUNT_MASK)
			i := 0
			for i < key_count && node.hashes[i] < attempt_key_hash {
				i++
			}
			if i < key_count && node.hashes[i] == attempt_key_hash { // target key found
				target_offs := int(node.kv_offs[i])
				if key != "" {
					if _, err := _verify_key(c.buf, key, key_tag_size, &target_offs); err == errCollision {
						break // try next probe
					} else if err != nil {
						panic(err)
					}
				}
				val_start_offs := target_offs
				if _, err := _verify_val(c.buf, target_offs); err != nil {
					panic(err)
				}
				if c.buf[val_start_offs] != val_typ {
					panic(fmt.Errorf("value type mismatch: %v != %v", c.buf[val_start_offs], val_typ))
				}
				return c.buf[val_start_offs+1:]
			}
			if node.child_offs[0] == 0 {
				panic(errors.New("key not found"))
			}
			next_node_offs := int(node.child_offs[i])
			node = cast[treeNode](c.buf, next_node_offs)

			if node_walks++; node_walks > LITE3_TREE_HEIGHT_MAX {
				panic(errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX"))
			}
		}
	}
	panic(errors.New("LITE3_HASH_PROBE_MAX limit reached"))
}

type iter struct {
	c         *Cursor
	gen       uint32
	node_offs [LITE3_TREE_HEIGHT_MAX + 1]uint32
	depth     int
	node_i    [LITE3_TREE_HEIGHT_MAX + 1]int
}

func (c *Cursor) Iter() *iter {
	node := c.node
	typ := node.gen_type & LITE3_NODE_TYPE_MASK
	if !(typ == LITE3_TYPE_OBJECT || typ == LITE3_TYPE_ARRAY) {
		panic(errors.New("not an array or object type"))
	}
	it := &iter{
		c:   c,
		gen: node.gen_type,
	}
	it.node_offs[0] = uint32(c.off)

	for node.child_offs[0] != 0 { // has children, travel down
		next_node_offs := node.child_offs[0]
		node = cast[treeNode](c.buf, int(next_node_offs))
		if it.depth++; it.depth > LITE3_TREE_HEIGHT_MAX {
			panic(errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX"))
		}
		it.node_offs[it.depth] = next_node_offs
		it.node_i[it.depth] = 0
	}
	return it
}

func (it *iter) Next() (key string, typ uint8, val []byte, err error) {
	if it.gen != cast[treeNode](it.c.buf, int(it.node_offs[0])).gen_type {
		return "", 0, nil, errors.New("buffer mutated")
	}
	node := cast[treeNode](it.c.buf, int(it.node_offs[it.depth]))
	nodeTyp := node.gen_type & LITE3_NODE_TYPE_MASK
	if !(nodeTyp == LITE3_TYPE_OBJECT || nodeTyp == LITE3_TYPE_ARRAY) {
		return "", 0, nil, errors.New("not an array or object type")
	}
	if it.depth == 0 && (it.node_i[it.depth] == int(node.size_kc&LITE3_NODE_KEY_COUNT_MASK)) { // key_count reached, done
		return "", 0, nil, io.EOF
	}

	target_offs := int(node.kv_offs[it.node_i[it.depth]])

	if nodeTyp == LITE3_TYPE_OBJECT { // write back key if not NULL
		key_start_offs := target_offs
		if _, err := _verify_key(it.c.buf, "", 0, &target_offs); err != nil {
			return "", 0, nil, err
		}
		key = string(it.c.buf[key_start_offs+1:][:(target_offs - key_start_offs - 2)]) // exclude C string terminator
	}
	val_start_offs := target_offs
	if _, err := _verify_val(it.c.buf, target_offs); err != nil {
		return "", 0, nil, err
	}
	typ, val = it.c.buf[val_start_offs], it.c.buf[val_start_offs+1:]
	it.node_i[it.depth]++

	for node.child_offs[it.node_i[it.depth]] != 0 { // has children, travel down
		next_node_offs := node.child_offs[it.node_i[it.depth]]
		node = cast[treeNode](it.c.buf, int(next_node_offs))
		if it.depth++; it.depth > LITE3_TREE_HEIGHT_MAX {
			return "", 0, nil, errors.New("node walks exceeded LITE3_TREE_HEIGHT_MAX")
		}
		it.node_offs[it.depth] = next_node_offs
		it.node_i[it.depth] = 0
	}
	for it.depth > 0 && it.node_i[it.depth] == int(node.size_kc&LITE3_NODE_KEY_COUNT_MASK) {
		it.depth--
		node = cast[treeNode](it.c.buf, int(it.node_offs[it.depth]))
	}
	return key, typ, val, nil
}
