package vm

import (
	"errors"
	"fmt"
	"math"
)

func init() {
	// float1 wraps a function of one Float
	float1 := func(name string, f func(float64) float64) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			x, err := argFloat(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return NewNumber(f(x)), nil
		}}
	}
	// toInt wraps a function that rounds a Float to an Int
	toInt := func(name string, f func(float64) float64) native {
		return native{name, 1, func(vm *VM, args []Value) (Value, error) {
			x, err := argFloat(args, 0)
			if err != nil {
				return NewNull(), err
			}
			r := f(x)
			if math.IsNaN(r) || r < float64(MinInt) || r > float64(MaxInt) {
				return NewNull(), fmt.Errorf("%v does not fit in an Int", x)
			}
			return NewInt(int64(r)), nil
		}}
	}
	float2 := func(name string, f func(a, b float64) float64) native {
		return native{name, 2, func(vm *VM, args []Value) (Value, error) {
			a, err := argFloat(args, 0)
			if err != nil {
				return NewNull(), err
			}
			b, err := argFloat(args, 1)
			if err != nil {
				return NewNull(), err
			}
			return NewNumber(f(a, b)), nil
		}}
	}

	registerNatives("std.math",
		native{"toFloat", 1, func(vm *VM, args []Value) (Value, error) {
			n, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			return NewNumber(float64(n)), nil
		}},
		toInt("floor", math.Floor),
		toInt("ceil", math.Ceil),
		toInt("round", math.Round),
		toInt("trunc", math.Trunc),
		native{"sqrt", 1, func(vm *VM, args []Value) (Value, error) {
			x, err := argFloat(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if x < 0 {
				return NewNull(), fmt.Errorf("the square root of %v is not a number", x)
			}
			return NewNumber(math.Sqrt(x)), nil
		}},
		float1("sin", math.Sin),
		float1("cos", math.Cos),
		float1("tan", math.Tan),
		float1("exp", math.Exp),
		native{"log", 1, func(vm *VM, args []Value) (Value, error) {
			x, err := argFloat(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if x <= 0 {
				return NewNull(), fmt.Errorf("the logarithm of %v is not a number", x)
			}
			return NewNumber(math.Log(x)), nil
		}},
		// base multiplied by itself exponent times, which fails as * does
		// when it goes past what an Int holds. It is here and not in the
		// library so that the failure is the program's line, where pow is
		// called
		native{"pow", 2, func(vm *VM, args []Value) (Value, error) {
			base, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			exponent, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			return powInt(base, exponent)
		}},
		float2("powf", math.Pow),
		float2("atan2", math.Atan2),
	)
}

// powInt is base to the power exponent, by squaring: about as many steps
// as the exponent has binary digits. A negative exponent gives 0, except
// for a base of 1 or -1.
func powInt(base, exponent int64) (Value, error) {
	if exponent < 0 {
		switch {
		case base == 1:
			return NewInt(1), nil
		case base == -1 && exponent%2 == 0:
			return NewInt(1), nil
		case base == -1:
			return NewInt(-1), nil
		}
		return NewInt(0), nil
	}
	result, factor := int64(1), base
	for left := exponent; left > 0; left /= 2 {
		ok := true
		if left%2 == 1 {
			if result, ok = mulInts(result, factor); !ok {
				return NewNull(), errors.New("integer overflow")
			}
		}
		if left > 1 {
			if factor, ok = mulInts(factor, factor); !ok {
				return NewNull(), errors.New("integer overflow")
			}
		}
	}
	return NewInt(result), nil
}
