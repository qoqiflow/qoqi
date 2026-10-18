import { describe, expect, it } from 'vitest';
import { doing } from './describe';
import { givenFields, keyParts, qoqi, outcomeOf, toPlain } from './qoqi';
import { initial } from '@/features/instances/fields';
import type { Data, Program, View } from './types';

// Values as the daemon gives them, written back as Qoqi and as what a
// start takes; the keys of steps read from their right; what an instance
// is doing, in words; and the values a form starts with.

const van: Data = { $type: 'std.result.Result.Ok', $variant: true, '0': 'van-3' };
const noVan: Data = {
  $type: 'std.result.Result.Err',
  $variant: true,
  '0': { $type: 'Problem.NoVan', $variant: true, '0': 'no van goes to Atlantis' },
};
const cart: Data = { $type: 'Cart', id: '1', amount: 4200, tags: ['a', 'b'], note: null };
const other: Data = { $type: 'Kind.Other', $variant: true, label: 'taxi', code: 7 };
const refused: Data = { $type: 'Decision.Refused', $variant: true, '0': 'ada', '1': 'too much' };

// A Dict as the program holds one: "a" was removed
const scores: Data = { $type: 'std.dict.Dict', keys: ['b', 'a', 'c'], values: [2, 1, 3], count: 2, index: null, dead: [false, true, false], removed: 1 };

describe('qoqi', () => {
  it('writes values as Qoqi does', () => {
    expect(qoqi(van)).toBe('Ok("van-3")');
    expect(qoqi(noVan)).toBe('Err(NoVan("no van goes to Atlantis"))');
    expect(qoqi(cart)).toBe('Cart { id: "1", amount: 4200, tags: ["a", "b"], note: null }');
    expect(qoqi(other)).toBe('Other { label: "taxi", code: 7 }');
    expect(qoqi({ $type: 'Decision.Approved', $variant: true })).toBe('Approved');
    expect(qoqi({ $float: 'NaN' })).toBe('NaN');
    expect(qoqi(null)).toBe('null');
    expect(qoqi(undefined)).toBe('');
    expect(qoqi(scores)).toBe('{"b": 2, "c": 3}');
    expect(qoqi({ $type: 'Expense', patience: { $type: 'std.time.Duration', ms: 5400000 } })).toBe('Expense { patience: 90.minutes() }');
    expect(qoqi({ $type: 'std.time.Duration', ms: 1500 })).toBe('1500.milliseconds()');
    const wide: Data = { $type: 'Wide.Many', $variant: true };
    for (let i = 11; i >= 0; i--) wide[i] = i;
    expect(qoqi(wide)).toBe('Many(0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11)');
  });

  it('gives back what a start takes', () => {
    expect(toPlain(cart)).toEqual({ id: '1', amount: 4200, tags: ['a', 'b'], note: null });
    expect(toPlain({ $type: 'Order', cart })).toEqual({ cart: { id: '1', amount: 4200, tags: ['a', 'b'], note: null } });
    expect(toPlain({ $type: 'Decision.Approved', $variant: true })).toBe('Approved');
    expect(toPlain(van)).toEqual({ Ok: 'van-3' });
    expect(toPlain(refused)).toEqual({ Refused: ['ada', 'too much'] });
    expect(toPlain(other)).toEqual({ Other: { label: 'taxi', code: 7 } });
    expect(toPlain(noVan)).toEqual({ Err: { NoVan: 'no van goes to Atlantis' } });
    expect(toPlain({ $type: 'Board', scores })).toEqual({ scores: { b: 2, c: 3 } });
    expect(toPlain({ $type: 'Signup', day: { $type: 'std.time.Duration', ms: 3000 } })).toEqual({ day: '3s' });
    expect(toPlain({ $type: 'std.time.Duration', ms: 172800000 })).toBe('2d');
  });

  it('tells Ok from Err', () => {
    expect(outcomeOf(van)).toBe('ok');
    expect(outcomeOf(noVan)).toBe('err');
    expect(outcomeOf(cart)).toBeNull();
    expect(outcomeOf(undefined)).toBeNull();
  });

  it('reads keys from their right', () => {
    expect(keyParts('order:1/fecc6515/0:0:reserve#1')).toEqual({ instance: 'order:1', birth: 'fecc6515', pass: '0', task: '0', step: 'reserve', n: 1 });
    expect(keyParts('retries-1/k7f2/0:0/task#2:charge#3')).toMatchObject({ instance: 'retries-1', task: '0/task#2', step: 'charge', n: 3 });
    expect(keyParts('a/b/c/k7f2/0:finish:undo#2')).toMatchObject({ instance: 'a/b/c', birth: 'k7f2', task: 'finish', step: 'undo', n: 2 });
    expect(keyParts('retries-1/k7f2/3:0:charge#1')).toMatchObject({ pass: '3', task: '0', step: 'charge', n: 1 });
  });

  it('leaves out the inboxes of what an instance was started with', () => {
    const fields: Data = {
      $type: 'Mailbox',
      owner: 'ada',
      notes: { $type: 'std.flow.Inbox', name: 'notes', capacity: 4 },
      cart,
    };
    expect(givenFields(fields)).toEqual({ $type: 'Mailbox', owner: 'ada', cart });
    expect(toPlain(givenFields(fields))).toEqual({ owner: 'ada', cart: { id: '1', amount: 4200, tags: ['a', 'b'], note: null } });
    expect(givenFields(undefined)).toEqual({});
  });
});

describe('doing', () => {
  const base: View = { id: 'order:1', flow: 'Order', status: 'waiting', birth: 'b' };
  const now = Date.parse('2026-10-16T12:00:00Z');

  it('says what an instance is doing', () => {
    expect(doing({ ...base, awaits: ['payment:1'] }, now)).toMatchObject({ text: 'waits for payment:1', link: 'payment:1', tone: 'wait' });
    expect(doing({ ...base, awaits: ['inbox:notes'] }, now)).toEqual({ text: 'waits for a message on notes', tone: 'wait', link: undefined });
    expect(doing({ ...base, awaits: ['payment:1', 'inbox:news'] }, now)).toMatchObject({ text: 'waits for payment:1, a message on news', link: 'payment:1' });
    expect(doing({ ...base, awaits: ['cancel:', 'inbox:answers'] }, now)).toEqual({ text: 'waits for a message on answers', tone: 'wait', link: undefined });
    expect(doing({ ...base, wakeAt: '2026-10-16T12:00:30Z' }, now)).toMatchObject({ text: 'wakes in 30 s', tone: 'wait' });
    expect(doing({ ...base, wakeAt: '2026-10-16T12:00:00Z' }, now).text).toBe('due now');
    expect(doing({ ...base, status: 'done', result: van }, now)).toMatchObject({ text: 'returned Ok("van-3")', tone: 'done' });
    expect(doing({ ...base, status: 'stopped', stopped: 'index out of bounds' }, now)).toMatchObject({ text: 'index out of bounds', tone: 'stop' });
    expect(doing({ ...base, status: 'failed', cancel: 'enough' }, now).text).toBe('cancelled: enough');
    expect(doing({ ...base, cancel: 'enough' }, now).text).toBe('cancelling: enough');
    expect(doing({ ...base, status: 'failed', cancel: 'looked at', ended: 'looked at' }, now)).toMatchObject({ text: 'ended as it was: looked at', tone: 'fail' });
  });
});

describe('initial', () => {
  const program: Program = {
    entry: 'shop.lum',
    flows: [],
    steps: [],
    files: [],
    types: {
      Cart: {
        kind: 'struct',
        name: 'Cart',
        text: 'Cart',
        fields: [
          { name: 'id', type: { kind: 'string', text: 'String' } },
          { name: 'amount', type: { kind: 'int', text: 'Int' } },
          { name: 'note', type: { kind: 'option', text: 'String?', elem: { kind: 'string', text: 'String' } } },
          { name: 'tags', type: { kind: 'array', text: '[String]', elem: { kind: 'string', text: 'String' } } },
          { name: 'speed', type: { kind: 'enum', name: 'Speed', text: 'Speed' } },
        ],
      },
      Speed: {
        kind: 'enum',
        name: 'Speed',
        text: 'Speed',
        variants: [{ name: 'Express', values: [{ name: '0', type: { kind: 'int', text: 'Int' } }] }, { name: 'Standard' }],
      },
      Loop: { kind: 'struct', name: 'Loop', text: 'Loop', fields: [{ name: 'next', type: { kind: 'struct', name: 'Loop', text: 'Loop' } }] },
    },
  };

  it('starts a form from the types', () => {
    expect(initial({ kind: 'struct', name: 'Cart', text: 'Cart' }, program)).toEqual({ id: '', amount: 0, note: null, tags: [], speed: { Express: 0 } });
    expect(initial({ kind: 'bool', text: 'Bool' }, program)).toBe(false);
  });

  it('stops at a type that holds itself', () => {
    expect(() => initial({ kind: 'struct', name: 'Loop', text: 'Loop' }, program)).not.toThrow();
  });
});
