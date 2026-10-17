package vm

import (
	"testing"
)

func TestHeapAllocation(t *testing.T) {
	heap := NewHeap(DefaultHeapSize)

	// Test string allocation
	str, shouldGC, err := heap.AllocString("hello world")
	if err != nil {
		t.Fatalf("Failed to allocate string: %v", err)
	}
	if str == nil {
		t.Fatal("String allocation returned nil")
	}
	if str.Data() != "hello world" {
		t.Errorf("Expected 'hello world', got '%s'", str.Data())
	}
	if shouldGC {
		t.Error("GC should not be needed after single small allocation")
	}

	// Test array allocation
	arr, shouldGC, err := heap.AllocArray(10)
	if err != nil {
		t.Fatalf("Failed to allocate array: %v", err)
	}
	if arr == nil {
		t.Fatal("Array allocation returned nil")
	}
	if arr.Len() != 0 {
		t.Errorf("Expected empty array, got length %d", arr.Len())
	}
	if shouldGC {
		t.Error("GC should not be needed after two small allocations")
	}

	// Test object allocation
	obj, shouldGC, err := heap.AllocObject()
	if err != nil {
		t.Fatalf("Failed to allocate object: %v", err)
	}
	if obj == nil {
		t.Fatal("Object allocation returned nil")
	}
	if len(obj.Keys()) != 0 {
		t.Errorf("Expected empty object, got %d keys", len(obj.Keys()))
	}
	if shouldGC {
		t.Error("GC should not be needed after three small allocations")
	}
}

func TestHeapExhaustion(t *testing.T) {
	// Create a tiny heap
	heap := NewHeap(1024)

	// Allocate until heap is exhausted
	count := 0
	for {
		_, _, err := heap.AllocString("this is a moderately long string to fill the heap")
		if err != nil {
			break
		}
		count++
		if count > 1000 {
			t.Fatal("Heap should have been exhausted")
		}
	}

	if count == 0 {
		t.Error("Should have been able to allocate at least one string")
	}
}

func TestArrayOperations(t *testing.T) {
	heap := NewHeap(DefaultHeapSize)
	arr, _, err := heap.AllocArray(5)
	if err != nil {
		t.Fatalf("Failed to allocate array: %v", err)
	}

	// Test append
	arr.Append(NewInt(1))
	arr.Append(NewInt(2))
	arr.Append(NewInt(3))

	if arr.Len() != 3 {
		t.Errorf("Expected length 3, got %d", arr.Len())
	}

	// Test get
	val := arr.Get(1)
	if !val.IsInt() || val.AsInt() != 2 {
		t.Errorf("Expected 2 at index 1, got %v", val)
	}

	// Test set
	arr.Set(1, NewInt(42))
	val = arr.Get(1)
	if !val.IsInt() || val.AsInt() != 42 {
		t.Errorf("Expected 42 at index 1, got %v", val)
	}

	// Test out of bounds get
	val = arr.Get(100)
	if !val.IsNull() {
		t.Errorf("Expected null for out of bounds access, got %v", val)
	}
}

func TestObjectOperations(t *testing.T) {
	heap := NewHeap(DefaultHeapSize)
	obj, _, err := heap.AllocObject()
	if err != nil {
		t.Fatalf("Failed to allocate object: %v", err)
	}

	// Test set
	obj.Set("name", NewInt(123))
	obj.Set("age", NewInt(25))

	// Test get
	val, ok := obj.Get("name")
	if !ok {
		t.Error("Expected to find 'name' field")
	}
	if !val.IsInt() || val.AsInt() != 123 {
		t.Errorf("Expected 123 for 'name', got %v", val)
	}

	// Test missing key
	val, ok = obj.Get("missing")
	if ok {
		t.Error("Should not find 'missing' field")
	}

	// Test keys
	keys := obj.Keys()
	if len(keys) != 2 {
		t.Errorf("Expected 2 keys, got %d", len(keys))
	}
}

func TestObjectLinkedList(t *testing.T) {
	heap := NewHeap(DefaultHeapSize)

	// Allocate multiple objects
	obj1, _, _ := heap.AllocString("first")
	obj2, _, _ := heap.AllocString("second")
	obj3, _, _ := heap.AllocString("third")

	// Check that they're all linked
	count := 0
	current := heap.objects
	found := [3]bool{false, false, false}

	for current != nil {
		count++
		if current == obj1.Header() {
			found[0] = true
		}
		if current == obj2.Header() {
			found[1] = true
		}
		if current == obj3.Header() {
			found[2] = true
		}
		current = current.Next
	}

	if count != 3 {
		t.Errorf("Expected 3 objects in list, got %d", count)
	}

	for i, f := range found {
		if !f {
			t.Errorf("Object %d not found in list", i)
		}
	}
}

func TestHeapStats(t *testing.T) {
	heap := NewHeap(1024 * 1024)

	if heap.BytesAllocated() != 0 {
		t.Errorf("Expected 0 bytes allocated initially, got %d", heap.BytesAllocated())
	}

	if heap.MaxBytes() != 1024*1024 {
		t.Errorf("Expected max bytes to be 1MB, got %d", heap.MaxBytes())
	}

	// Allocate something
	heap.AllocString("test")

	if heap.BytesAllocated() == 0 {
		t.Error("Expected non-zero bytes allocated after allocation")
	}

	if heap.ShouldCollect() {
		t.Error("GC should not be needed after single allocation")
	}
}
