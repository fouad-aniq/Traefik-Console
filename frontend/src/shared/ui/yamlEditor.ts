// Imported only when a YAML editor is mounted. No other page pays this bundle cost.
import { Compartment, EditorState } from '@codemirror/state';
import { EditorView, keymap, lineNumbers, highlightActiveLine, drawSelection, placeholder } from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { indentOnInput, indentUnit, syntaxHighlighting, HighlightStyle, bracketMatching } from '@codemirror/language';
import { yaml } from '@codemirror/lang-yaml';
import { tags } from '@lezer/highlight';
import { yamlBlockIndent } from './yamlIndent';

export interface YamlEditorSettings { disabled: boolean; invalid: boolean; required?: boolean; describedBy?: string; }
export function createYamlEditor(parent: HTMLElement, options: YamlEditorSettings & {
 id: string; label: string; value: string; placeholder: string; onchange: (value: string) => void;
}) {
 const settings = new Compartment();
 const attributes = (value: YamlEditorSettings) => [
  EditorState.readOnly.of(value.disabled), EditorView.editable.of(!value.disabled),
  EditorView.contentAttributes.of({ id: options.id, 'aria-label': options.label, 'aria-multiline': 'true', 'aria-readonly': String(value.disabled), 'aria-required': String(value.required ?? false), 'aria-invalid': String(value.invalid), ...(value.describedBy ? { 'aria-describedby': value.describedBy } : {}) })
 ];
 const view = new EditorView({ parent, state: EditorState.create({ doc: options.value, extensions: [
  yaml(), lineNumbers(), history(), drawSelection(), highlightActiveLine(), bracketMatching(),
  indentUnit.of('  '), indentOnInput(),
  keymap.of([{ key: 'Enter', run(view) {
   const selection = view.state.selection.main;
   const line = view.state.doc.lineAt(selection.head);
   const indent = selection.empty ? yamlBlockIndent(line.text.slice(0, selection.head - line.from)) : null;
   if (indent === null) return false;
   view.dispatch(view.state.replaceSelection('\n' + indent), { scrollIntoView: true });
   return true;
  } }, { key: 'Escape', run(view) { view.setTabFocusMode(2000); return true; } }, ...defaultKeymap, ...historyKeymap, indentWithTab]),
  placeholder(options.placeholder), settings.of(attributes(options)),
  syntaxHighlighting(HighlightStyle.define([
   { tag: [tags.propertyName, tags.definition(tags.propertyName)], color: 'var(--connection)' },
   { tag: [tags.string, tags.content], color: 'var(--success-text)' },
   { tag: [tags.number, tags.bool, tags.null], color: 'var(--accent)' },
   { tag: tags.comment, color: 'var(--text-muted)', fontStyle: 'italic' },
   { tag: [tags.meta, tags.punctuation], color: 'var(--text-muted)' }
  ])),
  EditorView.theme({
   '&': { backgroundColor: 'var(--surface)', color: 'var(--text)', fontSize: '14px' },
   '.cm-scroller': { fontFamily: 'var(--font-mono, ui-monospace, monospace)', lineHeight: '1.6', maxHeight: '440px', overflow: 'auto' },
   // The content and gutter carry the minimum height, so both fill the whole
   // bordered box even when the document is short.
   '.cm-content, .cm-gutter': { minHeight: '280px' },
   '.cm-content': { padding: '12px 0', caretColor: 'var(--text)' },
   '.cm-line': { padding: '0 12px' },
   '.cm-gutters': { backgroundColor: 'var(--surface-muted)', color: 'var(--text-muted)', borderRight: '1px solid var(--border)' },
   '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'transparent' },
   '&.cm-focused .cm-activeLine, &.cm-focused .cm-activeLineGutter': { backgroundColor: 'var(--surface-muted)' },
   '.cm-cursor': { borderLeftColor: 'var(--text)' },
   '&.cm-focused': { outline: 'none' },
   '&.cm-focused .cm-selectionBackground, .cm-selectionBackground': { backgroundColor: 'var(--accent-soft)' },
   '.cm-placeholder': { color: 'var(--text-muted)' }
  }),
  EditorView.updateListener.of(update => { if (update.docChanged) options.onchange(update.state.doc.toString()); })
 ] }) });
 return {
  setValue(value: string) { if (view.state.doc.toString() !== value) view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } }); },
  configure(value: YamlEditorSettings) { view.dispatch({ effects: settings.reconfigure(attributes(value)) }); },
  focus() { view.focus(); },
  destroy() { view.destroy(); }
 };
}
