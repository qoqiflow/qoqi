import { qoqi } from './qoqi';
import { relative } from './time';
import type { Place, View } from './types';

// What an instance is doing now, in a few words: what the lists show
// beside each one, and what the page of one says first.

export interface Doing {
  text: string;
  tone: 'wait' | 'run' | 'stop' | 'done' | 'fail';
  until?: number; // a moment it waits for
  link?: string; // an instance it waits for
}

/**
 * What an instance waits for, by name: other instances, its inboxes
 * ("inbox:notes"), and its own cancellation ("cancel:", a flow that hears
 * it), which goes without saying.
 */
export function awaited(inst: View): { instances: string[]; inboxes: string[] } {
  const all = (inst.awaits ?? []).filter((a) => !a.startsWith('cancel:'));
  return {
    instances: all.filter((a) => !a.startsWith('inbox:')),
    inboxes: all.filter((a) => a.startsWith('inbox:')).map((a) => a.slice('inbox:'.length)),
  };
}

/** What it waits for, in words. */
function waitText(inst: View): string {
  const { instances, inboxes } = awaited(inst);
  const parts = [...instances];
  if (inboxes.length) parts.push(`a message on ${inboxes.join(' or ')}`);
  return `waits for ${parts.join(', ')}`;
}

export function doing(inst: View, now: number): Doing {
  switch (inst.status) {
    case 'done':
      return { text: inst.phase === 'finish' && inst.cancel ? `cancelled: ${inst.cancel}` : `returned ${qoqi(inst.result)}`, tone: 'done' };
    case 'failed':
      if (inst.ended) return { text: `ended as it was: ${inst.ended}`, tone: 'fail' };
      return { text: inst.cancel ? `cancelled: ${inst.cancel}` : 'failed', tone: 'fail' };
    case 'stopped':
      return { text: inst.stopped || 'stopped, for a person', tone: 'stop' };
    case 'running':
      return { text: inst.phase === 'finish' ? 'undoing what it did' : 'running', tone: 'run' };
  }
  if (inst.cancel) return { text: `cancelling: ${inst.cancel}`, tone: 'run' };
  if (inst.phase === 'finish') return { text: 'undoing what it did', tone: 'run' };
  const { instances, inboxes } = awaited(inst);
  if (instances.length + inboxes.length > 0) {
    return { text: waitText(inst), tone: 'wait', link: instances[0] };
  }
  if (inst.wakeAt) {
    const at = Date.parse(inst.wakeAt);
    if (at <= now + 500) return { text: 'due now', tone: 'run' };
    return { text: `wakes ${relative(at, now)}`, tone: 'wait', until: at };
  }
  return { text: 'waits', tone: 'wait' };
}

/** What a task waits for, read from where it is. */
export function waitsFor(place: Place, inst: View, now: number): string {
  if (place.failed) return `failed: ${place.failed}`;
  if (!place.started) return 'made, not started yet';
  if (place.ready) return 'woken, waits for its turn';
  switch (place.waiting) {
    case 'step':
      return 'at a step, its request not sent yet';
    case 'suspendFor':
      return place.until ? `waits for the clock, ${relative(place.until, now)}` : 'waits for the clock';
    case 'suspend':
      if (awaited(inst).instances.length + awaited(inst).inboxes.length > 0) return waitText(inst);
      return 'waits for a task or a channel';
    case 'yield':
      return 'gave the others their turn';
  }
  return place.waiting ? `waits in ${place.waiting}` : 'waits';
}

/** Whether a frame is of the library: hidden unless asked for. */
export function isLibrary(file: string | undefined): boolean {
  return !file || file.startsWith('<std>');
}

export function shortFile(file: string | undefined): string {
  if (!file) return '';
  return file.split('/').pop() ?? file;
}
