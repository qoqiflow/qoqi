import { completeFromList, type Completion, type CompletionContext } from '@codemirror/autocomplete';
import { HighlightStyle, StreamLanguage, syntaxHighlighting, type StringStream } from '@codemirror/language';
import { EditorView } from '@codemirror/view';
import { tags as t } from '@lezer/highlight';

// Qoqi, as the editor colors it: the same colors as the pages that show
// code (components/code.tsx), read by a tokenizer of lines, with the
// texts of ${...} and the comments that run over several lines.

const keywords = [
  'fn', 'let', 'if', 'else', 'match', 'case', 'return', 'for', 'in', 'while', 'wait', 'step', 'impl', 'struct', 'enum',
  'import', 'pub', 'self', 'stage', 'migrate', 'default', 'trait', 'const', 'break', 'continue', 'defer', 'is', 'priv',
  'static', 'extern', 'by', 'as', 'type',
];
const atoms = ['true', 'false', 'null'];
const builtinTypes = ['Int', 'Float', 'Bool', 'String', 'Duration', 'Result', 'Option', 'Flow', 'Inbox', 'Ok', 'Err', 'Some', 'None'];

interface State {
  inComment: boolean;
  inString: '"' | '"""' | null;
  braces: number[]; // the depth of { at which each ${ of a text opened
  texts: ('"' | '"""')[]; // the texts those ${ are in, to go back to
  depth: number;
}

const word = /^[A-Za-z_][A-Za-z0-9_]*/;

function inText(stream: StringStream, state: State): string {
  const close = state.inString!;
  while (!stream.eol()) {
    if (stream.match('\\')) {
      stream.next();
      continue;
    }
    if (stream.match('${', false)) {
      if (stream.pos === stream.start) {
        stream.match('${');
        state.braces.push(state.depth);
        state.texts.push(close);
        state.depth++;
        state.inString = null;
        return 'meta';
      }
      return 'string';
    }
    if (stream.match(close)) {
      state.inString = null;
      return 'string';
    }
    stream.next();
  }
  return 'string';
}

const qoqi = StreamLanguage.define<State>({
  name: 'qoqi',
  startState: () => ({ inComment: false, inString: null, braces: [], texts: [], depth: 0 }),
  copyState: (s) => ({ ...s, braces: [...s.braces], texts: [...s.texts] }),
  token(stream, state) {
    if (state.inComment) {
      if (stream.skipTo('*/')) {
        stream.match('*/');
        state.inComment = false;
      } else stream.skipToEnd();
      return 'comment';
    }
    if (state.inString) return inText(stream, state);
    if (stream.eatSpace()) return null;
    if (stream.match('//')) {
      stream.skipToEnd();
      return 'comment';
    }
    if (stream.match('/*')) {
      state.inComment = true;
      return 'comment';
    }
    if (stream.match('"""') || stream.match('"')) {
      const quote = stream.current() as '"' | '"""';
      state.inString = quote;
      return inText(stream, state);
    }
    if (stream.match(/^\d[\d_]*(\.\d+)?/)) return 'number';
    if (stream.peek() === '{') {
      stream.next();
      state.depth++;
      return 'brace';
    }
    if (stream.peek() === '}') {
      stream.next();
      state.depth--;
      // The end of a ${...}: back in the text it was in
      if (state.braces.length && state.braces[state.braces.length - 1] === state.depth) {
        state.braces.pop();
        state.inString = state.texts.pop() ?? '"';
        return 'meta';
      }
      return 'brace';
    }
    const w = stream.match(word);
    if (w) {
      const text = (w as RegExpMatchArray)[0];
      if (keywords.includes(text)) return 'keyword';
      if (atoms.includes(text)) return 'atom';
      if (/^[A-Z]/.test(text)) return 'typeName';
      if (stream.match(/^\s*\(/, false)) return 'variableName.function';
      return 'variableName';
    }
    if (stream.match(/^(->|=>|==|!=|<=|>=|&&|\|\||\+\+|--|[-+*/%=<>!?.:,;()[\]|&])/)) return 'operator';
    stream.next();
    return null;
  },
  languageData: { commentTokens: { line: '//', block: { open: '/*', close: '*/' } }, closeBrackets: { brackets: ['(', '[', '{', '"'] } },
});

const colors = HighlightStyle.define([
  { tag: t.keyword, class: 'text-violet-600 dark:text-violet-300 font-medium' },
  { tag: t.atom, class: 'text-violet-600 dark:text-violet-300' },
  { tag: t.string, class: 'text-emerald-700 dark:text-emerald-300' },
  { tag: t.meta, class: 'text-orange-600 dark:text-orange-300' },
  { tag: t.number, class: 'text-orange-600 dark:text-orange-300' },
  { tag: t.comment, class: 'text-muted-foreground italic' },
  { tag: t.typeName, class: 'text-teal-700 dark:text-teal-300' },
  { tag: t.function(t.variableName), class: 'text-sky-700 dark:text-sky-300' },
  { tag: t.operator, class: 'text-muted-foreground' },
]);

/** The editor in the console's own tokens: its surfaces, its font, its selection. */
const look = EditorView.theme({
  '&': { height: '100%', backgroundColor: 'var(--card)', color: 'var(--foreground)' },
  '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.6' },
  '.cm-content': { caretColor: 'var(--primary)', padding: '12px 0' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--primary)', borderLeftWidth: '2px' },
  '.cm-gutters': { backgroundColor: 'var(--card)', color: 'var(--muted-foreground)', border: 'none', paddingLeft: '8px' },
  '.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--foreground)' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in oklch, var(--accent) 55%, transparent)' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': {
    backgroundColor: 'color-mix(in oklch, var(--primary) 22%, transparent) !important',
  },
  '.cm-matchingBracket': { backgroundColor: 'color-mix(in oklch, var(--primary) 18%, transparent)', outline: 'none' },
  '.cm-tooltip': { backgroundColor: 'var(--popover)', color: 'var(--popover-foreground)', border: '1px solid var(--border)', borderRadius: '8px' },
  '.cm-tooltip-autocomplete > ul > li[aria-selected]': { backgroundColor: 'var(--accent)', color: 'var(--foreground)' },
  '.cm-diagnostic': { fontFamily: 'var(--font-sans)', padding: '6px 10px' },
  '.cm-panels': { backgroundColor: 'var(--card)', color: 'var(--foreground)' },
  '.cm-searchMatch': { backgroundColor: 'color-mix(in oklch, var(--warning) 30%, transparent)' },
  '&.cm-focused': { outline: 'none' },
});

const snippets: Completion[] = [
  {
    label: 'step fn',
    type: 'keyword',
    detail: 'a step',
    apply: 'step fn name(argument: String) -> String {\n    return argument;\n}',
  },
  { label: 'stage', type: 'keyword', detail: 'a place it can move versions at', apply: 'stage name;' },
  { label: 'wait', type: 'keyword', detail: 'until a message, a child, a time', apply: 'wait ' },
  { label: 'flow.sleep', type: 'function', detail: 'wait for a while', apply: 'flow.sleep(1.hours())' },
  { label: 'flow.inbox', type: 'function', detail: 'an inbox of messages', apply: 'flow.inbox<String>(8)' },
];

/** What the editor offers as one types: the words of the language, those of the file, and some forms. */
function complete(context: CompletionContext) {
  const before = context.matchBefore(/[\w.]+/);
  if (!before && !context.explicit) return null;
  const seen = new Set<string>();
  const words: Completion[] = [];
  const text = context.state.doc.toString();
  for (const m of text.matchAll(/\b[A-Za-z_][A-Za-z0-9_]{2,}\b/g)) {
    if (!seen.has(m[0]) && !keywords.includes(m[0])) {
      seen.add(m[0]);
      words.push({ label: m[0], type: /^[A-Z]/.test(m[0]) ? 'type' : 'variable' });
    }
  }
  return completeFromList([
    ...snippets,
    ...keywords.map((k) => ({ label: k, type: 'keyword' })),
    ...builtinTypes.filter((k) => !seen.has(k)).map((k) => ({ label: k, type: 'type' })),
    ...words,
  ])(context);
}

export const qoqiLanguage = [qoqi, syntaxHighlighting(colors), look, qoqi.data.of({ autocomplete: complete })];
