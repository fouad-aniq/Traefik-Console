import { Parser } from 'yaml';

export type CodeToken = { text: string; kind: string };
/** CST offsets preserve the original review text, including whitespace and comments. */
export function yamlTokens(code: string): CodeToken[] {
 if (code.length > 200_000) return [{text:code,kind:''}];
 const ranges: {start:number;end:number;kind:string}[]=[];
 try {
  function visit(value: unknown, key=false, depth=0): void {
   if (depth>100) throw new Error('Token nesting limit');
   if (!value || typeof value!=='object') return;
   if (Array.isArray(value)) { for (const item of value) visit(item,key,depth+1); return; }
   const token=value as Record<string,unknown>;
   if (typeof token.source==='string' && typeof token.offset==='number') {
    const type=String(token.type);
    const kind=type==='comment'?'comment':key?'key':type.includes('quoted')||type==='block-scalar'?'string':type==='scalar'?/^(?:true|false|null|~|[-+]?\d+(?:\.\d+)?)$/i.test(token.source)?'literal':'string':['tag','anchor','alias'].includes(type)?'reference':'';
    if(kind)ranges.push({start:token.offset,end:token.offset+token.source.length,kind});
   }
   for(const [name,child] of Object.entries(token)) if(child && typeof child==='object') visit(child,name==='key'||key,depth+1);
  }
  for(const token of new Parser().parse(code))visit(token);
  ranges.sort((a,b)=>a.start-b.start||b.end-a.end);
  const result:CodeToken[]=[];let offset=0;
  for(const range of ranges){if(range.start<offset||range.end>code.length)continue;if(range.start>offset)result.push({text:code.slice(offset,range.start),kind:''});result.push({text:code.slice(range.start,range.end),kind:range.kind});offset=range.end;}
  if(offset<code.length)result.push({text:code.slice(offset),kind:''});
  return result;
 }catch{return [{text:code,kind:''}];}
}
export function diffLines(code:string):CodeToken[]{
 if(code.length>200_000)return [{text:code,kind:''}];
 return (code.match(/[^\n]*\n|[^\n]+$/g)||[]).map(text=>({text,kind:text.startsWith('+')?'added':text.startsWith('-')?'removed':text.startsWith('@@')?'hunk':''}));
}

/** One line of a unified diff with its YAML content highlighted, so +/- rows stay readable as code. */
export type DiffRow = { kind: 'added' | 'removed' | 'context' | 'meta'; marker: string; tokens: CodeToken[] };
export function diffRows(code: string): DiffRow[] {
 if (code.length > 200_000) return [{ kind: 'meta', marker: '', tokens: [{ text: code, kind: '' }] }];
 return code.split('\n').map((raw, index): DiffRow => {
  const line = raw.endsWith('\r') ? raw.slice(0, -1) : raw;
  const header = (index < 2 && (line.startsWith('--- ') || line.startsWith('+++ '))) || line.startsWith('@@') || line.startsWith('... ');
  if (header || !line) return { kind: 'meta', marker: '', tokens: [{ text: line, kind: header ? 'hunk' : '' }] };
  const marker = line[0];
  const kind = marker === '+' ? 'added' : marker === '-' ? 'removed' : 'context';
  const content = marker === '+' || marker === '-' || marker === ' ' ? line.slice(1) : line;
  return { kind, marker: kind === 'context' ? '' : marker, tokens: content.length > 2000 ? [{ text: content, kind: '' }] : yamlTokens(content) };
 });
}

export type DiffItem = { kind: 'row'; row: DiffRow } | { kind: 'gap'; id: number; rows: DiffRow[] };
const fileHeader = (r: DiffRow) => r.kind === 'meta' && r.tokens.length === 1 && r.tokens[0].kind === 'hunk' && /^(---|\+\+\+) /.test(r.tokens[0].text);
const isChange = (r: DiffRow) => r.kind === 'added' || r.kind === 'removed' || (r.kind === 'meta' && !fileHeader(r) && r.tokens.some((t) => t.text !== ''));
/**
 * GitHub-style view: keep changed rows with `context` unchanged rows around them and fold the rest
 * into expandable gaps. The `--- active / +++ proposed` file headers are dropped. A diff without
 * changes is returned untouched.
 */
export function collapseDiff(rows: DiffRow[], context = 3): DiffItem[] {
 const body = rows.filter((r) => !fileHeader(r));
 if (!body.some(isChange)) return body.map((row) => ({ kind: 'row', row }));
 const keep = new Array<boolean>(body.length).fill(false);
 body.forEach((r, i) => { if (isChange(r)) for (let k = Math.max(0, i - context); k <= Math.min(body.length - 1, i + context); k++) keep[k] = true; });
 const items: DiffItem[] = [];
 let hidden: DiffRow[] = [];
 const flush = () => { if (hidden.length) items.push({ kind: 'gap', id: items.length, rows: hidden }); hidden = []; };
 body.forEach((row, i) => { if (keep[i]) { flush(); items.push({ kind: 'row', row }); } else hidden.push(row); });
 flush();
 return items;
}
