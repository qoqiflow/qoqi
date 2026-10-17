import { Minus, Plus } from 'lucide-react';
import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { Textarea } from '@/components/ui/textarea';
import type { Data, Decl, Field, Program, Type, Variant } from '@/lib/types';
import { cn } from '@/lib/utils';

// A form for a value of the program, built from its type: a struct is its
// fields, an enum a choice of its variants and then what the one chosen
// holds, an optional value a switch. What it edits is the value as a
// person writes it to start a flow (see toPlain): the daemon types it.

type Obj = { [key: string]: Data };

/** The value a form starts with, for a type. */
export function initial(type: Type, program: Program, depth = 0): Data {
  switch (type.kind) {
    case 'int':
    case 'float':
      return 0;
    case 'bool':
      return false;
    case 'string':
      return '';
    case 'array':
      return [];
    case 'option':
      return null;
    case 'dict':
      return {};
    case 'duration':
      return '1h';
    case 'struct': {
      const decl = program.types[type.name ?? ''];
      if (!decl || depth > 6) return {};
      return Object.fromEntries((decl.fields ?? []).map((f) => [f.name, initial(f.type, program, depth + 1)]));
    }
    case 'enum': {
      const decl = program.types[type.name ?? ''];
      const first = decl?.variants?.[0];
      if (!first) return null;
      return variantValue(first, program, depth);
    }
  }
  return null;
}

function variantValue(variant: Variant, program: Program, depth: number, kept?: Data): Data {
  const values = variant.values ?? [];
  if (values.length === 0) return variant.name;
  if (variant.named) {
    const old = (kept && typeof kept === 'object' && !Array.isArray(kept) ? kept : {}) as Obj;
    return { [variant.name]: Object.fromEntries(values.map((f) => [f.name, old[f.name] ?? initial(f.type, program, depth + 1)])) };
  }
  if (values.length === 1) return { [variant.name]: kept ?? initial(values[0].type, program, depth + 1) };
  const old = Array.isArray(kept) ? kept : [];
  return { [variant.name]: values.map((f, i) => old[i] ?? initial(f.type, program, depth + 1)) };
}

/** The variant a value of an enum is, and what it holds. */
function chosen(value: Data, decl: Decl): { variant: Variant | undefined; held: Data | undefined } {
  if (typeof value === 'string') return { variant: decl.variants?.find((v) => v.name === value), held: undefined };
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const [name] = Object.keys(value);
    return { variant: decl.variants?.find((v) => v.name === name), held: (value as Obj)[name] };
  }
  return { variant: undefined, held: undefined };
}

function TypeTag({ type }: { type: Type }) {
  return <span className="font-mono text-[0.75rem] text-muted-foreground">{type.text}</span>;
}

export function FieldRow({
  field,
  value,
  onChange,
  program,
  path,
  error,
}: {
  field: Field;
  value: Data;
  onChange: (v: Data) => void;
  program: Program;
  path: string;
  error?: string;
}) {
  const inline = ['int', 'float', 'string', 'bool', 'duration'].includes(field.type.kind);
  const wrong = error && (error.startsWith(path + ' ') || error.startsWith(path + ':') || error.startsWith(path + '['));
  return (
    <div className={cn('grid gap-1.5', inline && 'sm:grid-cols-[11rem_1fr] sm:items-start sm:gap-4')}>
      <label htmlFor={path} className="flex flex-col pt-1.5">
        <span className="flex items-baseline gap-2">
          <span className="font-mono text-sm font-medium">{field.name}</span>
          <TypeTag type={field.type} />
        </span>
        {field.doc && <span className="text-xs text-muted-foreground">{field.doc}</span>}
      </label>
      <div className="min-w-0">
        <Editor type={field.type} value={value} onChange={onChange} program={program} path={path} error={error} invalid={!!wrong} />
      </div>
    </div>
  );
}

export function Editor({
  type,
  value,
  onChange,
  program,
  path,
  error,
  invalid,
}: {
  type: Type;
  value: Data;
  onChange: (v: Data) => void;
  program: Program;
  path: string;
  error?: string;
  invalid?: boolean;
}) {
  switch (type.kind) {
    case 'string':
      return (
        <Input id={path} value={typeof value === 'string' ? value : ''} aria-invalid={invalid} onChange={(e) => onChange(e.target.value)} />
      );
    case 'duration':
      // A length of time with its unit, read by the daemon: 90s, 1h30m, 2d
      return (
        <Input
          id={path}
          className="font-mono"
          value={typeof value === 'string' ? value : ''}
          placeholder="90s, 1h30m, 2d"
          aria-invalid={invalid}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    case 'int':
    case 'float':
      return <NumberInput id={path} value={value} float={type.kind === 'float'} invalid={invalid} onChange={onChange} />;
    case 'bool':
      return (
        <div className="flex h-9 items-center gap-3">
          <Switch id={path} checked={value === true} onCheckedChange={(on) => onChange(on)} />
          <span className="font-mono text-sm text-muted-foreground">{value === true ? 'true' : 'false'}</span>
        </div>
      );
    case 'option': {
      const set = value !== null && value !== undefined;
      return (
        <div className="grid gap-2">
          <div className="flex h-9 items-center gap-3">
            <Switch
              checked={set}
              onCheckedChange={(on) => onChange(on ? initial(type.elem!, program) : null)}
              aria-label={`give ${path} a value`}
            />
            <span className="font-mono text-sm text-muted-foreground">{set ? type.elem!.text : 'null'}</span>
          </div>
          {set && <Editor type={type.elem!} value={value} onChange={onChange} program={program} path={path} error={error} invalid={invalid} />}
        </div>
      );
    }
    case 'array': {
      const items = Array.isArray(value) ? value : [];
      return (
        <div className="grid gap-2">
          {items.map((item, i) => (
            <div key={i} className="flex items-start gap-2">
              <span className="w-6 pt-2 text-right font-mono text-xs text-muted-foreground">{i}</span>
              <div className="min-w-0 flex-1">
                <Editor
                  type={type.elem!}
                  value={item}
                  onChange={(v) => onChange(items.map((old, j) => (j === i ? v : old)))}
                  program={program}
                  path={`${path}[${i}]`}
                  error={error}
                />
              </div>
              <Button type="button" variant="ghost" size="icon" aria-label={`remove ${path}[${i}]`} onClick={() => onChange(items.filter((_, j) => j !== i))}>
                <Minus />
              </Button>
            </div>
          ))}
          <Button type="button" variant="outline" size="sm" className="w-fit" onClick={() => onChange([...items, initial(type.elem!, program)])}>
            <Plus /> Add {type.elem!.text}
          </Button>
        </div>
      );
    }
    case 'struct': {
      const decl = program.types[type.name ?? ''];
      if (!decl) return <JsonInput value={value} onChange={onChange} />;
      const object = (value && typeof value === 'object' && !Array.isArray(value) ? value : {}) as Obj;
      return (
        <fieldset className="grid gap-3 rounded-lg border bg-muted/30 p-3">
          <legend className="px-1 font-mono text-xs text-muted-foreground">{decl.text}</legend>
          {(decl.fields ?? []).map((f) => (
            <FieldRow
              key={f.name}
              field={f}
              value={object[f.name] ?? initial(f.type, program)}
              onChange={(v) => onChange({ ...object, [f.name]: v })}
              program={program}
              path={`${path}.${f.name}`}
              error={error}
            />
          ))}
        </fieldset>
      );
    }
    case 'enum': {
      const decl = program.types[type.name ?? ''];
      if (!decl) return <JsonInput value={value} onChange={onChange} />;
      return <VariantEditor decl={decl} value={value} onChange={onChange} program={program} path={path} error={error} />;
    }
  }
  return <JsonInput value={value} onChange={onChange} />;
}

function NumberInput({
  id,
  value,
  float,
  invalid,
  onChange,
}: {
  id: string;
  value: Data;
  float: boolean;
  invalid?: boolean;
  onChange: (v: Data) => void;
}) {
  const [text, setText] = useState(String(typeof value === 'number' ? value : 0));
  return (
    <Input
      id={id}
      inputMode={float ? 'decimal' : 'numeric'}
      className="font-mono"
      value={text}
      aria-invalid={invalid}
      onChange={(e) => {
        setText(e.target.value);
        const n = Number(e.target.value.replace(/[_\s]/g, ''));
        // A text that is not a number is sent as it is: the daemon says why
        onChange(e.target.value.trim() !== '' && Number.isFinite(n) ? n : e.target.value);
      }}
    />
  );
}

function VariantEditor({
  decl,
  value,
  onChange,
  program,
  path,
  error,
}: {
  decl: Decl;
  value: Data;
  onChange: (v: Data) => void;
  program: Program;
  path: string;
  error?: string;
}) {
  const variants = decl.variants ?? [];
  const { variant, held } = chosen(value, decl);
  const pick = (name: string) => {
    const next = variants.find((v) => v.name === name);
    if (next) onChange(variantValue(next, program, 0));
  };
  const values = variant?.values ?? [];
  return (
    <div className="grid gap-3">
      {variants.length <= 4 ? (
        <div role="radiogroup" aria-label={decl.text} className="flex flex-wrap gap-1.5">
          {variants.map((v) => (
            <button
              key={v.name}
              type="button"
              role="radio"
              aria-checked={v.name === variant?.name}
              onClick={() => pick(v.name)}
              className={cn(
                'rounded-md border px-3 py-1.5 text-left font-mono text-sm transition-colors',
                v.name === variant?.name ? 'border-primary bg-primary/10 text-primary' : 'hover:bg-accent',
              )}
            >
              {v.name}
              {(v.values?.length ?? 0) > 0 && (
                <span className="text-muted-foreground">({(v.values ?? []).map((f) => (v.named ? `${f.name}: ` : '') + f.type.text).join(', ')})</span>
              )}
            </button>
          ))}
        </div>
      ) : (
        <Select value={variant?.name} onValueChange={pick}>
          <SelectTrigger className="w-full font-mono">
            <SelectValue placeholder={`a ${decl.text}`} />
          </SelectTrigger>
          <SelectContent>
            {variants.map((v) => (
              <SelectItem key={v.name} value={v.name} className="font-mono">
                {v.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
      {variant && values.length > 0 && (
        <div className="grid gap-3 border-l-2 border-primary/30 pl-3">
          {values.map((f, i) => {
            const current = variant.named
              ? ((held ?? {}) as Obj)[f.name]
              : values.length === 1
                ? held
                : Array.isArray(held)
                  ? held[i]
                  : undefined;
            const set = (v: Data) => {
              if (variant.named) onChange({ [variant.name]: { ...((held ?? {}) as Obj), [f.name]: v } });
              else if (values.length === 1) onChange({ [variant.name]: v });
              else {
                const next = Array.isArray(held) ? held.slice() : values.map((g) => initial(g.type, program));
                next[i] = v;
                onChange({ [variant.name]: next });
              }
            };
            return (
              <FieldRow
                key={f.name}
                field={variant.named ? f : { ...f, name: values.length === 1 ? 'value' : `value ${i}` }}
                value={current ?? initial(f.type, program)}
                onChange={set}
                program={program}
                path={`${path}: ${variant.name}.${f.name}`}
                error={error}
              />
            );
          })}
        </div>
      )}
    </div>
  );
}

function JsonInput({ value, onChange }: { value: Data; onChange: (v: Data) => void }) {
  const [text, setText] = useState(() => JSON.stringify(value ?? null, null, 2));
  const [bad, setBad] = useState(false);
  return (
    <Textarea
      className="min-h-20 font-mono text-[0.8125rem]"
      value={text}
      aria-invalid={bad}
      onChange={(e) => {
        setText(e.target.value);
        try {
          onChange(JSON.parse(e.target.value));
          setBad(false);
        } catch {
          setBad(true);
        }
      }}
    />
  );
}
