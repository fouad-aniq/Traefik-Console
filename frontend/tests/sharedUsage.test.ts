import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import ReferenceCount from '../src/shared/ui/ReferenceCount.svelte';
import RelatedItemsList from '../src/shared/ui/RelatedItemsList.svelte';
import ValueTooltip from '../src/shared/ui/ValueTooltip.svelte';
import { createRawSnippet } from 'svelte';

afterEach(() => cleanup());

const text = (value: string) => createRawSnippet(() => ({ render: () => `<span>${value}</span>` }));

describe('ReferenceCount', () => {
	it('opens the dialog from the count', async () => {
		const onclick = vi.fn();
		render(ReferenceCount, { count: 2, onclick });
		const button = screen.getByRole('button', { name: '2 resources' });
		expect(button.getAttribute('aria-haspopup')).toBe('dialog');
		await fireEvent.click(button);
		expect(onclick).toHaveBeenCalled();
	});
	it('says "1 resource" whatever the kind of the referencing item', () => {
		render(ReferenceCount, { count: 1, onclick: () => {} });
		expect(screen.getByRole('button', { name: '1 resource' })).toBeTruthy();
	});
	it('shows an empty state instead of 0, with nothing to click', () => {
		const { container } = render(ReferenceCount, { count: 0, onclick: () => {} });
		expect(container.textContent?.trim()).toBe('Not used');
		expect(screen.queryByRole('button')).toBeNull();
	});
});

describe('RelatedItemsList', () => {
	const items = [
		{
			name: 'portainer',
			kind: 'route' as const,
			summary: 'Host(`p.example`)',
			facts: ['websecure', 'TLS'],
			path: '/routers/portainer',
			editPath: '/routers/portainer/edit',
		},
		{ name: 'zone', kind: 'domain' as const, summary: 'example.com', path: '/domains/zone' },
	];
	it('renders each item with its kind, summary and facts, without count line or View/Edit buttons', () => {
		const { container } = render(RelatedItemsList, { items, navigate: () => {} });
		expect(container.querySelectorAll('li').length).toBe(2);
		expect(container.textContent).toContain('Host(`p.example`)');
		expect(container.textContent).toContain('websecure');
		expect(container.querySelector('.count')).toBeNull();
		expect(container.textContent).not.toMatch(/Used by \d/);
		expect(screen.queryByRole('button', { name: /^View/ })).toBeNull();
		expect(screen.queryByRole('button', { name: /^Edit/ })).toBeNull();
	});
	it('opens the item view when its name is clicked (editing happens from the view)', async () => {
		const navigate = vi.fn();
		render(RelatedItemsList, { items, navigate });
		await fireEvent.click(screen.getByRole('button', { name: 'portainer' }));
		expect(navigate).toHaveBeenCalledWith('/routers/portainer');
	});
	it('uses the caller empty text, or the default one', () => {
		const { container, unmount } = render(RelatedItemsList, {
			items: [],
			navigate: () => {},
			empty: 'No route uses this.',
		});
		expect(container.textContent).toContain('No route uses this.');
		unmount();
		const second = render(RelatedItemsList, { items: [], navigate: () => {} });
		expect(second.container.textContent).toContain('Nothing uses this yet.');
	});
	it('shows the loading state', () => {
		render(RelatedItemsList, { items, loading: true, navigate: () => {} });
		expect(screen.getByRole('status').textContent).toContain('Loading');
	});
});

describe('ValueTooltip with an empty value', () => {
	it('is not interactive and never describes a placeholder', () => {
		const { container } = render(ValueTooltip, { text: '', truncate: true, children: text('—') });
		const trigger = container.querySelector('button.tooltip-trigger')!;
		expect(trigger.classList.contains('interactive')).toBe(false);
		expect(trigger.getAttribute('aria-describedby')).toBeNull();
	});
	it('stays clickable when it opens something', async () => {
		const onactivate = vi.fn();
		const { container } = render(ValueTooltip, { text: '', children: text('—'), onactivate });
		const trigger = container.querySelector('button.tooltip-trigger')!;
		expect(trigger.classList.contains('interactive')).toBe(true);
		await fireEvent.click(trigger);
		expect(onactivate).toHaveBeenCalled();
	});
	it('describes a value that differs from what is shown', async () => {
		const { container } = render(ValueTooltip, { text: 'full value', children: text('full…') });
		await new Promise((r) => setTimeout(r, 0));
		expect(container.querySelector('button.tooltip-trigger')!.getAttribute('aria-describedby')).toBeTruthy();
	});
});
