/** Shared table contracts (kept out of the generics-bearing component so
 * svelte2tsx can type-check exports reliably). */
export interface DataTableColumn {
	id: string;
	label: string;
	sortable?: boolean;
	/** Optional CSS column width; unspecified columns share the remaining space. */
	width?: string;
	/** Keep a descriptive heading for assistive technology without visible text. */
	hideLabel?: boolean;
}

export interface DataTableFilter {
	id: string;
	label: string;
	/** value '' means the All default */
	options: { value: string; label: string }[];
}

export type SortState = { column: string; direction: 'asc' | 'desc' | 'none' };

/** One action in a table row; see RowActions.svelte. */
export interface RowAction {
	label: string;
	onclick: () => void;
	variant?: 'primary' | 'secondary' | 'danger';
	disabled?: boolean;
	/** Extra words for assistive technology only, e.g. the row's name. */
	context?: string;
}
