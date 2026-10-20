import { describe, expect, it } from 'vitest';
import { shortName } from './utils';

describe('shortName', () => {
  it('is the last part of a flow a module declares', () => {
    expect(shortName('flows.conversation.Conversation')).toBe('Conversation');
  });

  it('is the name itself of a flow of the entry file', () => {
    expect(shortName('Order')).toBe('Order');
  });
});
