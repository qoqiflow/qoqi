package compiler

import "testing"

// Numbers written in the program are read by the instructions that use
// them (MULK, JNLTK and the like), and x.toFloat() is an instruction of its
// own. The program goes through every one of them, from methods, matches,
// loops and closures; what it prints is what it printed before they existed.
func TestNumbersWrittenInTheProgram(t *testing.T) {
	out, _, err := runProgram(t, numbersProgram)
	if err != nil {
		t.Fatalf("the program failed: %v", err)
	}
	if out != numbersPrinted {
		t.Errorf("printed\n%s\nexpected\n%s", out, numbersPrinted)
	}
}

const numbersProgram = `struct Account {
    balance: Int;
    rate: Float;
}

impl Account {
    fn grown(years: Int) -> Float {
        let total = self.balance.toFloat();

        for _ in 0..years {
            total = total * (1.0 + self.rate) - 2.5;
        }

        return total;
    }
}

enum Reading {
    Celsius(Float),
    Count(Int),
}

fn describe(reading: Reading) -> String {
    return match reading {
        case Reading.Celsius(c) if c > 37.5: "fever ${c * 1.8 + 32.0}";
        case Reading.Celsius(c): "normal ${c * 1.8 + 32.0}";
        case Reading.Count(n) if n >= 100000: "many ${n / 1000}k";
        case Reading.Count(n): "few ${n % 70000}";
    };
}

fn main() -> Int {
    let big = 100000;
    println(big * 3, 3 * big, big + 70000, 70000 + big, big - 70000, 70000 - big);
    println(big / 70000, 70000 / big, big % 70000, 70000 % big, -big % 70000);

    let f = 2.5;
    println(f * 4.0, 4.0 * f, f + 0.25, 0.25 + f, f - 0.5, 0.5 - f, f / 0.5, 0.5 / f);
    println(big * 0.5, 0.5 * big, big + 0.5, big / 4.0, 7 % 2, -7 % 2);
    println(7.5 > 2.5, 2.5 < 7.5, big == 100000, 100000 == big, big != 100000, f >= 2.5, f <= 2.0);
    println(big > 99999.5, 100000.0 == big, big < -1.5, -0.0 == 0.0);

    // In loops and their conditions
    let n = 1;
    let count = 0;

    while n < 1000000 {
        n = n * 3 + 100001;
        count += 1;
    }

    println(n, count);

    let total = 0.0;

    for i in 0..1000 {
        if i.toFloat() * 0.75 > 600.0 {
            break;
        }

        total = total + i.toFloat() / 8.0;
    }

    println(total);

    // In tests that decide where to go
    let seen: [String] = [];

    for x in [1.5, 2.5, 100000.0, 70000.0] {
        if x >= 2.5 {
            seen.push("ge");
        }

        if x <= 2.5 {
            seen.push("le");
        }

        if x == 100000.0 {
            seen.push("eq");
        }

        if x != 70000.0 {
            seen.push("ne");
        }
    }

    println(seen.join(" "));

    // In closures given to the library
    let scale = fn(x: Float) -> Float {
        return x * 1500.0 / 4.0;
    };

    println([1.0, 2.5, -4.0].map(scale));
    println([150000, 2, 99999, 100001].filter(x -> x > 99999.5).map(x -> x * 70000 % 1000003));

    // In methods, matches and texts
    let account = Account { balance: 1000, rate: 0.05 };
    println(account.grown(10), Account { balance: 70000, rate: 1.5 }.grown(3));

    for reading in [
        Reading.Celsius(38.5),
        Reading.Celsius(36.6),
        Reading.Count(250000),
        Reading.Count(140001),
    ] {
        println(describe(reading));
    }

    // Floats at their edges
    let huge = 1.0e300;
    let two = 2.0;
    println(huge * 1.0e10, -huge * 1.0e10, 1.0 / 0.0 > 1.0e308, two.sqrt() * two.sqrt() == 2.0);
    return 0;
}
`

const numbersPrinted = `300000 300000 170000 170000 30000 -30000
1 0 30000 70000 -30000
10 10 2.75 2.75 2 -2 5 0.2
50000 50000 100000.5 25000 1 -1
true true true true false true false
true true false true
1300040 3
40050
le ne ge le ne ge eq ne ge
[375, 937.5, -1500]
[968503, 49000]
1597.4498954385701 1.093725625e+06
fever 101.3
normal 97.88000000000001
many 250k
many 140k
+Inf -Inf true false
`
