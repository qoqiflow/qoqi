import type { Field, FlowInfo } from '../../lib/types';

/** A value of a field, as Qoqi writes it, for a first test to start from. */
function sample(f: Field): string | null {
  switch (f.type.kind) {
    case 'string':
      return `"${f.name}"`;
    case 'int':
      return '1';
    case 'float':
      return '1.0';
    case 'bool':
      return 'false';
    case 'duration':
      return '1.minutes()';
    case 'array':
      return '[]';
    case 'option':
      return 'null';
    default:
      return null;
  }
}

/** A first test file for a program: its flow tried, its step answered for. */
export function firstTest(program: string, flow: string, info?: FlowInfo): string {
  const given = (info?.fields ?? []).filter((f) => !f.default && !f.optional);
  const fields = given.map((f) => `${f.name}: ${sample(f) ?? '/* a ' + f.type.text + ' */'}`).join(', ');
  // A flow with inboxes waits for its messages: it has not ended yet
  const status = (info?.inboxes?.length ?? 0) > 0 ? 'waiting' : 'done';
  return `import std.flow;
import std.test;
import ${program};
import ${program}.{${flow}};

// A test is a function testSomething(): flow.test runs ${flow} here, on a
// store in memory, its clock moved by hand (trial.advance(2.days())), its
// steps replaced by functions of the test (trial.replace(step, fn ...)).
// What a test uses of ${program} is pub there.

fn testItEnds() {
    let trial = flow.test(${flow} {${fields ? ' ' + fields + ' ' : ''}});

    trial.go();
    test.equal(trial.status(), "${status}");
}
`;
}
