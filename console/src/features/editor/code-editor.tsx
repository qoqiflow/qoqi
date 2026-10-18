import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap } from '@codemirror/autocomplete';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { bracketMatching, indentOnInput, indentUnit } from '@codemirror/language';
import { lintGutter, setDiagnostics, type Diagnostic } from '@codemirror/lint';
import { highlightSelectionMatches, searchKeymap } from '@codemirror/search';
import { Compartment, EditorState, type Extension } from '@codemirror/state';
import {
  crosshairCursor,
  drawSelection,
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
  rectangularSelection,
} from '@codemirror/view';
import { forwardRef, useEffect, useImperativeHandle, useRef } from 'react';
import type { Problem } from '@/lib/types';
import { qoqiLanguage } from './language';

// A file of a program, being written: CodeMirror, with Qoqi colored, what
// the compiler said under the words it said it of, and the keys of an
// editor (⌘S saves, ⇧⌥F formats).

export interface CodeEditorHandle {
  goto: (line: number, column: number) => void;
  focus: () => void;
}

interface Props {
  /** Which file: a new one starts a state of its own, with its own history. */
  path: string;
  value: string;
  onChange: (text: string) => void;
  problems: Problem[];
  fontSize: number;
  wrap: boolean;
  readOnly?: boolean;
  onSave: () => void;
  onFormat: () => void;
}

function diagnosticsOf(state: EditorState, problems: Problem[]): Diagnostic[] {
  const doc = state.doc;
  return problems.flatMap((p) => {
    if (p.line < 1 || p.line > doc.lines) return [];
    const line = doc.line(p.line);
    const from = Math.min(line.to, line.from + Math.max(0, p.column - 1));
    let to = p.endColumn ? Math.min(line.to, line.from + p.endColumn - 1) : from;
    if (to <= from) {
      // A word at least: the one the position is on
      const rest = line.text.slice(from - line.from);
      to = from + Math.max(1, rest.match(/^\w+/)?.[0].length ?? 1);
      to = Math.min(to, line.to);
    }
    const severity = p.severity === 'error' ? 'error' : p.severity === 'warning' ? 'warning' : 'info';
    return [{ from, to: Math.max(to, from), severity, message: p.message + (p.suggestion ? `\n${p.suggestion}` : ''), source: p.code }];
  });
}

export const CodeEditor = forwardRef<CodeEditorHandle, Props>(function CodeEditor(
  { path, value, onChange, problems, fontSize, wrap, readOnly, onSave, onFormat },
  ref,
) {
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const states = useRef(new Map<string, EditorState>());
  const latest = useRef({ onChange, onSave, onFormat });
  latest.current = { onChange, onSave, onFormat };
  const looks = useRef(new Compartment());
  const lock = useRef(new Compartment());

  const lookOf = (size: number, wrapping: boolean): Extension => [
    EditorView.theme({ '&': { fontSize: `${size}px` } }),
    wrapping ? EditorView.lineWrapping : [],
  ];

  const make = (text: string): EditorState =>
    EditorState.create({
      doc: text,
      extensions: [
        lineNumbers(),
        highlightActiveLineGutter(),
        history(),
        drawSelection(),
        indentOnInput(),
        bracketMatching(),
        closeBrackets(),
        autocompletion(),
        rectangularSelection(),
        crosshairCursor(),
        highlightActiveLine(),
        highlightSelectionMatches(),
        lintGutter(),
        indentUnit.of('    '),
        EditorState.tabSize.of(4),
        keymap.of([
          { key: 'Mod-s', preventDefault: true, run: () => (latest.current.onSave(), true) },
          { key: 'Shift-Alt-f', preventDefault: true, run: () => (latest.current.onFormat(), true) },
          ...closeBracketsKeymap,
          ...defaultKeymap,
          ...searchKeymap,
          ...historyKeymap,
          ...completionKeymap,
          indentWithTab,
        ]),
        qoqiLanguage,
        looks.current.of(lookOf(fontSize, wrap)),
        lock.current.of(EditorState.readOnly.of(!!readOnly)),
        EditorView.updateListener.of((u) => {
          if (u.docChanged) latest.current.onChange(u.state.doc.toString());
        }),
      ],
    });

  // One view; a state for each file, kept as one goes from file to file
  useEffect(() => {
    const v = new EditorView({ state: make(value), parent: host.current! });
    view.current = v;
    states.current.set(path, v.state);
    return () => {
      v.destroy();
      view.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const shown = useRef(path);
  useEffect(() => {
    const v = view.current;
    if (!v || shown.current === path) return;
    states.current.set(shown.current, v.state);
    shown.current = path;
    const kept = states.current.get(path);
    v.setState(kept && kept.doc.toString() === value ? kept : make(value));
    v.dispatch({ effects: [looks.current.reconfigure(lookOf(fontSize, wrap)), lock.current.reconfigure(EditorState.readOnly.of(!!readOnly))] });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path]);

  // A text changed from outside (formatted, reverted): the editor takes it
  useEffect(() => {
    const v = view.current;
    if (!v || v.state.doc.toString() === value) return;
    v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } });
  }, [value]);

  useEffect(() => {
    view.current?.dispatch({ effects: looks.current.reconfigure(lookOf(fontSize, wrap)) });
  }, [fontSize, wrap]);

  useEffect(() => {
    view.current?.dispatch({ effects: lock.current.reconfigure(EditorState.readOnly.of(!!readOnly)) });
  }, [readOnly]);

  useEffect(() => {
    const v = view.current;
    if (v) v.dispatch(setDiagnostics(v.state, diagnosticsOf(v.state, problems)));
  }, [problems, path]);

  useImperativeHandle(ref, () => ({
    goto: (line, column) => {
      const v = view.current;
      if (!v) return;
      const l = v.state.doc.line(Math.min(Math.max(1, line), v.state.doc.lines));
      const at = Math.min(l.to, l.from + Math.max(0, column - 1));
      v.dispatch({ selection: { anchor: at }, effects: EditorView.scrollIntoView(at, { y: 'center' }) });
      v.focus();
    },
    focus: () => view.current?.focus(),
  }));

  return <div ref={host} className="h-full min-h-0 overflow-hidden" />;
});
