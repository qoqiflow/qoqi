import type { Data } from './types';

// Values as Qoqi writes them: Ok("van-3"), Err(NoVan("no van")),
// Cart { id: "1", amount: 4200 }, read from the data the API gives.

export type Piece = { text: string; kind: 'type' | 'string' | 'number' | 'keyword' | 'punct' | 'name' };

type Obj = { [key: string]: Data };

function isObject(value: Data): value is Obj {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** The last part of a type's name: std.result.Result.Ok is Ok, Problem.NoVan is NoVan. */
export function typeName(type: string): string {
  return type.split('.').pop() ?? type;
}

function valuesOf(value: Obj): [string, Data][] {
  return Object.keys(value)
    .filter((key) => !key.startsWith('$'))
    .map((key) => [key, value[key]]);
}

/** Whether a variant's values are by place, as the program holds them: "0", "1", ... */
function positional(entries: [string, Data][]): boolean {
  return entries.every(([key]) => /^\d+$/.test(key));
}

const durationUnits: [string, string, number][] = [
  ['days', 'd', 86400000],
  ['hours', 'h', 3600000],
  ['minutes', 'm', 60000],
  ['seconds', 's', 1000],
  ['milliseconds', 'ms', 1],
];

/** A length of time as Qoqi writes it, in the largest unit it is a whole number of: 90.minutes(). */
export function durationLiteral(ms: number): string {
  if (ms === 0) return '0.seconds()';
  const [name, , size] = durationUnits.find(([, , size]) => ms % size === 0) ?? durationUnits[4];
  const n = ms / size;
  return n < 0 ? `(${n}).${name}()` : `${n}.${name}()`;
}

/** A length of time as a start takes it: 90m, 2d, 1500ms. */
export function durationText(ms: number): string {
  if (ms === 0) return '0s';
  const [, unit, size] = durationUnits.find(([, , size]) => ms % size === 0) ?? durationUnits[4];
  return `${ms / size}${unit}`;
}

/** The pieces of a value written as Qoqi, for coloring. */
export function pieces(value: Data, out: Piece[] = []): Piece[] {
  if (value === null || value === undefined) {
    out.push({ text: 'null', kind: 'keyword' });
  } else if (typeof value === 'string') {
    out.push({ text: JSON.stringify(value), kind: 'string' });
  } else if (typeof value === 'number') {
    out.push({ text: String(value), kind: 'number' });
  } else if (typeof value === 'boolean') {
    out.push({ text: String(value), kind: 'keyword' });
  } else if (Array.isArray(value)) {
    out.push({ text: '[', kind: 'punct' });
    value.forEach((item, i) => {
      if (i > 0) out.push({ text: ', ', kind: 'punct' });
      pieces(item, out);
    });
    out.push({ text: ']', kind: 'punct' });
  } else if ('$float' in value) {
    out.push({ text: String(value.$float), kind: 'number' });
  } else if (value.$type === 'std.time.Duration' && typeof value.ms === 'number') {
    out.push({ text: durationLiteral(value.ms), kind: 'number' });
  } else if (value.$type === 'std.dict.Dict' && Array.isArray(value.keys)) {
    // A Dict as its literal, the removed keys left out
    const values = Array.isArray(value.values) ? value.values : [];
    const dead = Array.isArray(value.dead) ? value.dead : [];
    out.push({ text: '{', kind: 'punct' });
    let first = true;
    value.keys.forEach((key, i) => {
      if (dead[i] === true) return;
      if (!first) out.push({ text: ', ', kind: 'punct' });
      first = false;
      pieces(key, out);
      out.push({ text: ': ', kind: 'punct' });
      pieces(values[i] ?? null, out);
    });
    out.push({ text: '}', kind: 'punct' });
  } else {
    const type = typeof value.$type === 'string' ? typeName(value.$type) : '';
    const entries = valuesOf(value);
    if (value.$variant) {
      out.push({ text: type, kind: 'type' });
      if (entries.length === 0) return out;
      if (positional(entries)) {
        out.push({ text: '(', kind: 'punct' });
        entries.forEach(([, item], i) => {
          if (i > 0) out.push({ text: ', ', kind: 'punct' });
          pieces(item, out);
        });
        out.push({ text: ')', kind: 'punct' });
        return out;
      }
    } else if (type) {
      out.push({ text: type, kind: 'type' });
    }
    out.push({ text: type ? ' { ' : '{ ', kind: 'punct' });
    entries.forEach(([key, item], i) => {
      if (i > 0) out.push({ text: ', ', kind: 'punct' });
      out.push({ text: key, kind: 'name' });
      out.push({ text: ': ', kind: 'punct' });
      pieces(item, out);
    });
    out.push({ text: ' }', kind: 'punct' });
  }
  return out;
}

/** A value written as Qoqi. */
export function qoqi(value: Data | undefined): string {
  if (value === undefined) return '';
  return pieces(value)
    .map((p) => p.text)
    .join('');
}

/**
 * A value of the program as a person writes it to start a flow: structs
 * without their "$type", a variant by its name, {"Refused": "why"} when it
 * holds a value. It is what the daemon reads back (flowd.Conform).
 */
export function toPlain(value: Data): Data {
  if (Array.isArray(value)) return value.map(toPlain);
  if (!isObject(value)) return value;
  if (value.$type === 'std.time.Duration' && typeof value.ms === 'number') return durationText(value.ms);
  if (value.$type === 'std.dict.Dict' && Array.isArray(value.keys)) {
    const values = Array.isArray(value.values) ? value.values : [];
    const dead = Array.isArray(value.dead) ? value.dead : [];
    return Object.fromEntries(
      value.keys.flatMap((key, i) => (dead[i] === true || typeof key !== 'string' ? [] : [[key, toPlain(values[i] ?? null)]])),
    );
  }
  const entries = valuesOf(value);
  if (value.$variant && typeof value.$type === 'string') {
    const name = typeName(value.$type);
    if (entries.length === 0) return name;
    if (positional(entries)) {
      const items = entries.map(([, item]) => toPlain(item));
      return { [name]: items.length === 1 ? items[0] : items };
    }
    return { [name]: Object.fromEntries(entries.map(([key, item]) => [key, toPlain(item)])) };
  }
  return Object.fromEntries(entries.map(([key, item]) => [key, toPlain(item)]));
}

/** Whether a value is Ok(...) or Err(...) of std.result, or neither. */
export function outcomeOf(value: Data | undefined): 'ok' | 'err' | null {
  if (!isObject(value as Data) || typeof (value as Obj).$type !== 'string') return null;
  const type = (value as Obj).$type as string;
  if (type === 'std.result.Result.Ok') return 'ok';
  if (type === 'std.result.Result.Err') return 'err';
  return null;
}

/** Parts of a step key: name/birth/pass:task:step#n, read from its right. */
export function keyParts(key: string): { instance: string; birth: string; pass: string; task: string; step: string; n: number } {
  const slash = key.split('/');
  let i = slash.length - 2;
  while (i >= 1 && (slash[i].includes(':') || slash[i].includes('#'))) i--;
  const instance = slash.slice(0, i).join('/');
  const birth = slash[i] ?? '';
  const rest = slash.slice(i + 1).join('/');
  const firstColon = rest.indexOf(':');
  const pass = rest.slice(0, firstColon);
  const after = rest.slice(firstColon + 1);
  const lastColon = after.lastIndexOf(':');
  const task = after.slice(0, lastColon);
  const call = after.slice(lastColon + 1);
  const [step, count] = call.split('#');
  return { instance, birth, pass, task, step, n: Number(count) || 1 };
}

/** The fields an instance was started with, without its inboxes: the daemon makes those. */
export function givenFields(fields: Data | undefined): Data {
  if (!fields || typeof fields !== 'object' || Array.isArray(fields)) return fields ?? {};
  return Object.fromEntries(
    Object.entries(fields).filter(([, v]) => !(v && typeof v === 'object' && !Array.isArray(v) && v.$type === 'std.flow.Inbox')),
  );
}
