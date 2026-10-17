package vm

import (
	"fmt"
	"unsafe"
)

const (
	// DefaultHeapSize is the most a program may hold at once: 256MB
	DefaultHeapSize = 256 * 1024 * 1024
	// DefaultGCStart is how much is allocated before the first collection
	DefaultGCStart = 1024 * 1024
	// A heap smaller than DefaultGCStart collects when it is 80% full
	DefaultGCThreshold = 0.8
)

// Heap manages heap-allocated objects.
type Heap struct {
	// Head of the linked list of all objects
	objects *ObjHeader

	// Memory statistics
	bytesAllocated uint64
	nextGC         uint64
	collections    int // number of collections run so far

	// Configuration
	maxBytes uint64
	minGC    uint64 // the collection threshold never drops below this

	// Runs a collection. Set by the VM, which knows the roots; called when
	// an allocation would exceed maxBytes, before giving up.
	collect func()

	// onFree, when set, is told of every object the collector frees. Tests
	// use it to spoil freed objects, so that using one shows.
	onFree func(*ObjHeader)

	// The shape of an object without fields: where every object starts.
	// There is one per type the objects are values of; rootShape is that
	// of the plain objects, which are of none.
	rootShape *Shape
	roots     map[shapeKind]*Shape
}

// shapeKind is what the values of a type have in common from the start.
type shapeKind struct {
	typeName string
	variant  bool
}

// RootShape returns the shape the values of a type start with: a struct
// by its name, or a variant of an enum by "Enum.Variant".
func (h *Heap) RootShape(typeName string, variant bool) *Shape {
	kind := shapeKind{typeName, variant}
	if shape, ok := h.roots[kind]; ok {
		return shape
	}
	shape := newRootShape(typeName, variant)
	h.roots[kind] = shape
	return shape
}

const (
	valueSize   = uint64(unsafe.Sizeof(Value(0)))
	pointerSize = uint64(unsafe.Sizeof(uintptr(0)))

	// maxArrayCapacity bounds what one array may be asked to hold, well
	// above what any heap limit allows: a number beyond it is a mistake,
	// not a request to be tried.
	maxArrayCapacity = 1 << 40
)

// sizeOf is what an object counts for against the limit. It is asked when
// the object is made and again when it is freed, and what grew it in
// between (see grow) has to be counted the same way here: the two must
// agree, or the count drifts away from what is really in use.
func sizeOf(header *ObjHeader) uint64 {
	switch header.Type {
	case ObjTypeString:
		obj := (*StringObj)(unsafe.Pointer(header))
		// A text not written out yet is counted without the bytes it will
		// have: they are counted when it is (see flatten)
		if obj.rope() != nil {
			return uint64(unsafe.Sizeof(StringObj{}))
		}
		return uint64(unsafe.Sizeof(StringObj{})) + uint64(len(obj.data))
	case ObjTypeArray:
		obj := (*ArrayObj)(unsafe.Pointer(header))
		return uint64(unsafe.Sizeof(ArrayObj{})) + uint64(cap(obj.elements))*valueSize
	case ObjTypeObject:
		obj := (*ObjectObj)(unsafe.Pointer(header))
		return uint64(unsafe.Sizeof(ObjectObj{})) + uint64(cap(obj.slots))*valueSize
	case ObjTypeFunction:
		obj := (*FunctionObj)(unsafe.Pointer(header))
		return uint64(unsafe.Sizeof(FunctionObj{})) + uint64(len(obj.upvalues))*pointerSize
	case ObjTypeExternFunction:
		obj := (*ExternFunctionObj)(unsafe.Pointer(header))
		return uint64(unsafe.Sizeof(ExternFunctionObj{})) + uint64(len(obj.name))
	case ObjTypeUpvalue:
		return uint64(unsafe.Sizeof(UpvalueObj{}))
	case ObjTypeIndex:
		return (*IndexObj)(unsafe.Pointer(header)).counted
	}
	return uint64(unsafe.Sizeof(ObjHeader{}))
}

// NewHeap creates a new heap with the given size limit.
func NewHeap(maxBytes uint64) *Heap {
	if maxBytes == 0 {
		maxBytes = DefaultHeapSize
	}

	// Collect for the first time at DefaultGCStart, or when a smaller heap
	// is nearly full. After each collection the threshold is set to twice
	// what survived (see sweep), so the cost of collecting stays
	// proportional to what is allocated.
	start := uint64(float64(maxBytes) * DefaultGCThreshold)
	if start > DefaultGCStart {
		start = DefaultGCStart
	}

	plain := newRootShape("", false)
	return &Heap{
		objects:        nil,
		bytesAllocated: 0,
		nextGC:         start,
		maxBytes:       maxBytes,
		minGC:          start,
		rootShape:      plain,
		roots:          map[shapeKind]*Shape{{}: plain},
	}
}

// reserve makes sure size more bytes fit under the limit, collecting
// garbage first if they do not.
func (h *Heap) reserve(size uint64) error {
	if h.bytesAllocated+size > h.maxBytes && h.collect != nil {
		h.collect()
	}
	if h.bytesAllocated+size > h.maxBytes {
		return fmt.Errorf("out of memory: %d bytes in use, %d more requested, limit %d", h.bytesAllocated, size, h.maxBytes)
	}
	return nil
}

// grow accounts for an existing object getting larger: an array gaining
// capacity, an object gaining a field. It reports whether a collection is
// due.
func (h *Heap) grow(size uint64) (bool, error) {
	if err := h.reserve(size); err != nil {
		return false, err
	}
	h.bytesAllocated += size
	return h.ShouldCollect(), nil
}

// Collections returns how many garbage collections have run.
func (h *Heap) Collections() int {
	return h.collections
}

// track puts a new object on the list of objects and counts it. Room for
// it has been reserved before it was made.
func (h *Heap) track(obj Obj) {
	header := obj.Header()
	header.Next = h.objects
	h.objects = header
	h.bytesAllocated += sizeOf(header)
}

// AllocString allocates a string object.
// Returns the object and a boolean indicating if GC should run.
func (h *Heap) AllocString(s string) (*StringObj, bool, error) {
	if err := h.reserve(uint64(unsafe.Sizeof(StringObj{})) + uint64(len(s))); err != nil {
		return nil, false, err
	}
	obj := &StringObj{header: ObjHeader{Type: ObjTypeString}, data: s}
	h.track(obj)
	return obj, h.ShouldCollect(), nil
}

// A short text made by joining two comes with its bytes, in one piece: the
// object and the bytes are one allocation, and the two texts are copied
// straight to where they stay. Joining is what a program building a text
// does over and over, mostly to short ones.
type (
	stringOf16 struct {
		StringObj
		room [16]byte
	}
	stringOf32 struct {
		StringObj
		room [32]byte
	}
	stringOf64 struct {
		StringObj
		room [64]byte
	}
)

// joined makes the text of two texts one after the other.
func joined(a, b string) *StringObj {
	var obj *StringObj
	var room []byte
	switch size := len(a) + len(b); {
	case size == 0:
		return &StringObj{header: ObjHeader{Type: ObjTypeString}}
	case size <= 16:
		whole := &stringOf16{}
		obj, room = &whole.StringObj, whole.room[:size]
	case size <= 32:
		whole := &stringOf32{}
		obj, room = &whole.StringObj, whole.room[:size]
	case size <= 64:
		whole := &stringOf64{}
		obj, room = &whole.StringObj, whole.room[:size]
	default:
		return &StringObj{header: ObjHeader{Type: ObjTypeString}, data: a + b}
	}
	copy(room, a)
	copy(room[len(a):], b)
	obj.header.Type = ObjTypeString
	// The text is the bytes that came with the object, which nothing
	// writes to again
	obj.data = unsafe.String(&room[0], len(room))
	return obj
}

// AllocJoined allocates the text of two texts one after the other.
// Returns the object and a boolean indicating if GC should run.
func (h *Heap) AllocJoined(a, b string) (*StringObj, bool, error) {
	if err := h.reserve(uint64(unsafe.Sizeof(StringObj{})) + uint64(len(a)+len(b))); err != nil {
		return nil, false, err
	}
	obj := joined(a, b)
	h.track(obj)
	return obj, h.ShouldCollect(), nil
}

// AllocRope allocates the text of two texts one after the other, kept as
// the two until its bytes are asked for. It knows its length in characters
// from theirs.
func (h *Heap) AllocRope(left, right *StringObj) (*StringObj, bool, error) {
	size := left.ByteLen() + right.ByteLen()
	if err := h.reserve(uint64(unsafe.Sizeof(StringObj{}))); err != nil {
		return nil, false, err
	}
	obj := &StringObj{header: ObjHeader{Type: ObjTypeString}, side: &stringSide{left: left, right: right, size: size, heap: h}}
	obj.runes = int32(left.RuneLen() + right.RuneLen())
	obj.ascii = left.ascii && right.ascii
	obj.measured = true
	h.track(obj)
	return obj, h.ShouldCollect(), nil
}

// AllocArray allocates an array object.
// Returns the object and a boolean indicating if GC should run.
//
// The room is reserved before the elements are: an array larger than the
// limit is refused without the memory ever being asked of the machine.
func (h *Heap) AllocArray(capacity int) (*ArrayObj, bool, error) {
	if capacity < 0 || capacity > maxArrayCapacity {
		return nil, false, fmt.Errorf("out of memory: an array of %d elements cannot be made", capacity)
	}
	if err := h.reserve(uint64(unsafe.Sizeof(ArrayObj{})) + uint64(capacity)*valueSize); err != nil {
		return nil, false, err
	}
	obj := &ArrayObj{header: ObjHeader{Type: ObjTypeArray}, elements: make([]Value, 0, capacity)}
	h.track(obj)
	return obj, h.ShouldCollect(), nil
}

// AllocObject allocates a plain object: a value of no type, without
// fields yet.
// Returns the object and a boolean indicating if GC should run.
func (h *Heap) AllocObject() (*ObjectObj, bool, error) {
	return h.AllocObjectOf(h.rootShape, 0)
}

// AllocObjectOf allocates a value of a type (see RootShape) with room for
// a number of fields, which it does not have yet.
// Returns the object and a boolean indicating if GC should run.
func (h *Heap) AllocObjectOf(shape *Shape, fields int) (*ObjectObj, bool, error) {
	if err := h.reserve(uint64(unsafe.Sizeof(ObjectObj{})) + uint64(roomFor(fields))*valueSize); err != nil {
		return nil, false, err
	}
	obj := newObject(shape, fields)
	h.track(obj)
	return obj, h.ShouldCollect(), nil
}

// AllocFunction allocates a function object.
// Returns the object and a boolean indicating if GC should run.
func (h *Heap) AllocFunction(chunk *Chunk, protoIdx int, upvalueCount int) (*FunctionObj, bool, error) {
	// Validate proto index
	if protoIdx < 0 || protoIdx >= len(chunk.Prototypes) {
		return nil, false, fmt.Errorf("invalid prototype index: %d (chunk has %d prototypes)", protoIdx, len(chunk.Prototypes))
	}
	if upvalueCount < 0 {
		return nil, false, fmt.Errorf("invalid number of captured variables: %d", upvalueCount)
	}
	if err := h.reserve(uint64(unsafe.Sizeof(FunctionObj{})) + uint64(upvalueCount)*pointerSize); err != nil {
		return nil, false, err
	}
	obj := &FunctionObj{
		header:   ObjHeader{Type: ObjTypeFunction},
		chunk:    chunk,
		protoIdx: protoIdx,
		proto:    &chunk.Prototypes[protoIdx],
		upvalues: make([]*UpvalueObj, upvalueCount),
	}
	h.track(obj)
	return obj, h.ShouldCollect(), nil
}

// AllocExternFunction allocates an external function object.
// Returns the object and a boolean indicating if GC should run.
func (h *Heap) AllocExternFunction(name string, function ExternFunc, argCount int) (*ExternFunctionObj, bool, error) {
	if err := h.reserve(uint64(unsafe.Sizeof(ExternFunctionObj{})) + uint64(len(name))); err != nil {
		return nil, false, err
	}
	obj := &ExternFunctionObj{
		header:   ObjHeader{Type: ObjTypeExternFunction},
		name:     name,
		function: function,
		argCount: argCount,
	}
	h.track(obj)
	return obj, h.ShouldCollect(), nil
}

// AllocUpvalue allocates an upvalue object.
// Returns the object and a boolean indicating if GC should run.
// The upvalue starts in the "open" state, naming a register on a stack.
func (h *Heap) AllocUpvalue(stack *regStack, slot int) (*UpvalueObj, bool, error) {
	if err := h.reserve(uint64(unsafe.Sizeof(UpvalueObj{}))); err != nil {
		return nil, false, err
	}
	obj := &UpvalueObj{
		header: ObjHeader{Type: ObjTypeUpvalue},
		stack:  stack,
		slot:   slot,
		closed: NewNull(),
	}
	h.track(obj)
	return obj, h.ShouldCollect(), nil
}

// indexEntrySize is what one key of an index is counted for: its place in
// the table, and the text of the key, which the index shares with the
// dictionary's own keys.
const indexEntrySize = 48

// AllocIndex allocates the index of a dictionary, with room for a number
// of keys.
func (h *Heap) AllocIndex(keys int) (*IndexObj, error) {
	size := uint64(unsafe.Sizeof(IndexObj{})) + uint64(keys)*indexEntrySize
	if err := h.reserve(size); err != nil {
		return nil, err
	}
	obj := &IndexObj{header: ObjHeader{Type: ObjTypeIndex}, at: make(map[string]int32, keys), counted: size}
	h.track(obj)
	return obj, nil
}

// ShouldCollect returns true if GC should run.
func (h *Heap) ShouldCollect() bool {
	return h.bytesAllocated >= h.nextGC
}

// BytesAllocated returns current heap usage.
func (h *Heap) BytesAllocated() uint64 {
	return h.bytesAllocated
}

// MaxBytes returns heap size limit.
func (h *Heap) MaxBytes() uint64 {
	return h.maxBytes
}
