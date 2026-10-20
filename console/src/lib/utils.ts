import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export const formatDate = (dateString: string | null) => {
  if (!dateString) return '-';
  return new Date(dateString).toLocaleDateString('fr-FR', {
    year: 'numeric',
    month: 'long',
    day: 'numeric',
  });
};

/** A flow's name as people say it: the last part of a name its module
 * gives, Conversation of flows.conversation.Conversation. */
export function shortName(name: string): string {
  return name.slice(name.lastIndexOf('.') + 1);
}
