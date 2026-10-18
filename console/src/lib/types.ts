// What the daemon's API gives, as internal/flowd writes it.

export type Status = 'waiting' | 'running' | 'stopped' | 'done' | 'failed';

export const statuses: Status[] = ['waiting', 'running', 'stopped', 'done', 'failed'];

/** A value as the program writes it: JSON, with "$type" on structs and variants. */
export type Data = null | boolean | number | string | Data[] | { [key: string]: Data };

export interface Undo {
  step: string;
  argument: Data;
}

export interface View {
  id: string;
  flow: string;
  status: Status;
  birth: string;
  wakeAt?: string;
  fields?: Data;
  result?: Data;
  stopped?: string;
  steps?: Record<string, number>;
  phase?: string;
  undos?: Undo[];
  cancel?: string;
  parent?: string;
  children?: string[];
  awaits?: string[];
  stage?: string;
  pass?: number;
  version?: string; // the bytecode it runs, by its fingerprint
  replaying?: boolean; // resumed, and not yet at its next stage
  ended?: string; // why a person ended it as it was
}

export interface PlaceFrame {
  function: string;
  file?: string;
  line?: number;
  column?: number;
}

export interface Place {
  task: string;
  waiting?: string;
  until?: number;
  ready?: boolean;
  started: boolean;
  failed?: string;
  frames?: PlaceFrame[];
}

export interface Kept {
  key: string;
  result: Data;
  undos?: Undo[];
}

export interface Line {
  at: string;
  id?: string;
  text: string;
}

export interface Detail extends View {
  tasks: Place[];
  kept: Kept[];
  tries?: Record<string, number>;
  retries?: Record<string, string>;
  output: Line[];
  unanswered?: string[]; // the steps that left, their answer not kept: a person may answer them
}

export interface FlowCounts {
  name: string;
  instances: Partial<Record<Status, number>>;
}

export interface Type {
  kind:
    | 'int'
    | 'float'
    | 'bool'
    | 'string'
    | 'void'
    | 'any'
    | 'array'
    | 'option'
    | 'dict'
    | 'struct'
    | 'enum'
    | 'union'
    | 'function'
    | 'duration'
    | 'inbox'
    | 'other';
  name?: string;
  text: string;
  elem?: Type;
  of?: Type[];
}

export interface Field {
  name: string;
  type: Type;
  default?: boolean;
  doc?: string;
  optional?: boolean;
}

export interface Variant {
  name: string;
  values?: Field[];
  named?: boolean;
}

export interface Position {
  file: string;
  line: number;
}

export interface Decl {
  kind: 'struct' | 'enum';
  name: string;
  text: string;
  fields?: Field[];
  variants?: Variant[];
  doc?: string;
  at?: Position;
}

export interface FlowInfo {
  name: string;
  returns: Type;
  fields: Field[] | null;
  inboxes?: Field[]; // the fields messages come in by: no start gives them
  doc?: string;
  at?: Position;
  run?: Position;
}

export interface StepInfo {
  name: string;
  params: Field[] | null;
  returns: Type;
  once?: boolean;
  doc?: string;
  at?: Position;
}

export interface Program {
  entry: string;
  flows: FlowInfo[];
  steps: StepInfo[];
  types: Record<string, Decl>;
  files: string[];
}

export interface Source {
  file: string;
  lines: string[];
}

export interface Event {
  kind: 'instance' | 'step' | 'output' | 'deploy';
  at: string;
  id?: string;
  instance?: View;
  step?: string;
  key?: string;
  once?: boolean;
  text?: string;
}

export interface DryRun {
  flow: string;
  fields: Data;
  taken?: View;
}

/** What a resume would do, found without doing it. */
export interface ResumePlan {
  took: string[] | null; // the steps it would find done, by their keys
  next: string[] | null; // what it would do then
  forget?: string[]; // the undos whose Err it would forget, to run them again
  stopped?: string; // what would stop it again
}

/** A version of a program deployed to the daemon. */
export interface DeployView {
  seq: number;
  program: string;
  version: string;
  at: string;
  flows: string[] | null;
  files?: string[];
  changes?: string[]; // what it changed of the stages of the version before
  moving: number; // instances that were to move to it when it was deployed
  alive: number; // instances alive that run it now
  serving: boolean; // the version a new instance starts on
  removed?: boolean; // the program taken away, here
  sources?: Record<string, string>;
}

export interface DiffLine {
  kind: ' ' | '+' | '-';
  text: string;
}

export interface Hunk {
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  lines: DiffLine[];
}

export interface FileDiff {
  file: string;
  status: 'added' | 'removed' | 'changed' | 'same';
  added: number;
  removed: number;
  hunks?: Hunk[];
}

/** What changed from one deploy to another. */
export interface Diff {
  from?: DeployView;
  to: DeployView;
  files: FileDiff[];
  stages?: string[];
}

/** Who asks: a person signed in, or a holder of the daemon's token. */
export interface Principal {
  user?: string;
  token?: boolean;
  renew?: boolean; // a password given them: changed before anything else
}

export interface AuthState {
  accounts: boolean; // whether the daemon keeps people at all
  me?: Principal;
  prefs?: Prefs;
}

/** A person's choices of the pages, kept by the daemon. */
export interface Prefs {
  density?: 'comfortable' | 'compact';
  editorFontSize?: number;
  editorWrap?: boolean;
  weekStart?: 0 | 1;
}

export interface User {
  name: string;
  created: number;
  renew?: boolean; // to choose a password of their own at their next sign in
}

/** A program as the editor lists it. */
export interface WorkspaceProgram {
  program: string;
  files: string[];
  draft: boolean; // changed in the console, not deployed
  deployed?: string; // the version served
  updated?: number;
  author?: string;
}

export interface WorkspaceFile {
  file: string;
  text: string;
  updated?: number;
  author?: string;
}

/** A program opened in the editor: its draft, or what was deployed of it. */
export interface Opened {
  program: string;
  entry: string;
  draft: boolean;
  deployed?: string;
  files: WorkspaceFile[];
}

/** A diagnostic of the compiler. */
export interface Problem {
  severity: 'error' | 'warning' | 'info' | 'hint' | string;
  code?: string;
  message: string;
  file: string;
  line: number;
  column: number;
  endColumn?: number;
  suggestion?: string;
}

export interface Checked {
  ok: boolean;
  flows: string[];
  problems: Problem[];
}

/** What a deploy did, or would do. */
export interface Report {
  seq?: number;
  program: string;
  version: string;
  before?: string;
  new?: boolean;
  same?: boolean;
  changes?: string[];
  said?: string[];
  moving: number;
}

export interface DraftDeploy extends Checked {
  report?: Report;
  why?: string[];
}

export interface DayStats {
  day: string;
  started: number;
  done: number;
  failed: number;
  stopped: number;
  steps: number;
}

export interface FlowStats {
  flow: string;
  program: string;
  started: number;
  done: number;
  failed: number;
  stopped: number;
  p50: number;
  p95: number;
  max: number;
}

export interface StepRuns {
  hour?: string;
  flow: string;
  step: string;
  runs: number;
}

export interface Stats {
  from: string;
  to: string;
  days: DayStats[];
  flows: FlowStats[];
  steps: StepRuns[];
  hours: number[][];
}
