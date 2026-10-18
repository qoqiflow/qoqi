// What a new program starts as, in the editor: each compiles as it is,
// and deploys; the flow is named after the program, Orders for orders.

export interface Template {
  id: string;
  title: string;
  hint: string;
  text: (flow: string) => string;
}

export function flowNameOf(program: string): string {
  return program
    .split('_')
    .filter(Boolean)
    .map((part) => part[0].toUpperCase() + part.slice(1))
    .join('');
}

export const templates: Template[] = [
  {
    id: 'step',
    title: 'A flow with a step',
    hint: 'Its step runs once: what it returns is kept, and it is not run again after a crash.',
    text: (flow) => `// ${flow}: a flow the daemon runs, through restarts and new versions.
import std.flow.{Flow};

pub struct ${flow} {
    customer: String;
}

impl Flow<String> for ${flow} {
    fn run() -> String {
        let greeting = greet(self.customer);

        return greeting;
    }
}

// A step: the only way a flow reaches the world. What it returns is kept
// under its key, and it is not done again.
pub step fn greet(name: String) -> String {
    return "hello \${name}";
}

fn main() {}
`,
  },
  {
    id: 'inbox',
    title: 'A flow that waits for messages',
    hint: 'It sleeps in the store until a message comes to its inbox, for as long as it takes.',
    text: (flow) => `// ${flow}: waits for notes, counts them, and ends when told to stop.
import std.flow;
import std.flow.{Flow, Inbox};

pub struct ${flow} {
    owner: String;
    notes: Inbox<String> = flow.inbox<String>(8);
}

impl Flow<String> for ${flow} {
    fn run() -> String {
        let seen = 0;

        while true {
            // A stage: where a new version of the program takes it up
            stage listening;

            let note = wait self.notes.receive();

            if note == "stop" {
                return "\${self.owner} \${seen}";
            }

            seen = seen + record(note);
        }
    }
}

pub step fn record(note: String) -> Int {
    println("noted: \${note}");

    return 1;
}

fn main() {}
`,
  },
  {
    id: 'empty',
    title: 'An empty program',
    hint: 'Only the imports of a flow: write it from there.',
    text: (flow) => `import std.flow.{Flow};

pub struct ${flow} {
}

impl Flow<String> for ${flow} {
    fn run() -> String {
        return "";
    }
}

fn main() {}
`,
  },
];
