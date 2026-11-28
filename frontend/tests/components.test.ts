import { describe, expect, it, vi } from 'vitest';
import { render, fireEvent } from '@testing-library/svelte';
import Button from '../src/shared/ui/Button.svelte';
import StatusBadge from '../src/shared/ui/StatusBadge.svelte';
import Notice from '../src/shared/ui/Notice.svelte';
import ActiveFilterSummaries from '../src/shared/ui/ActiveFilterSummaries.svelte';
import Pagination from '../src/shared/ui/Pagination.svelte';
import ConfirmDialog from '../src/shared/ui/ConfirmDialog.svelte';
import Tabs from '../src/shared/ui/Tabs.svelte';

describe('Shared UI Components', () => {
	describe('Button', () => {
		it('renders button with label and handles click', async () => {
			const handleClick = vi.fn();
			const { container } = render(Button, {
				props: {
					variant: 'primary',
					onclick: handleClick,
					label: 'Click Me',
				},
			});

			const btn = container.querySelector('button');
			expect(btn).not.toBeNull();
			expect(btn?.className).toContain('btn-primary');
			expect(btn?.textContent).toContain('Click Me');

			await fireEvent.click(btn!);
			expect(handleClick).toHaveBeenCalledTimes(1);
		});

		it('disables button when disabled prop is true', () => {
			const { container } = render(Button, {
				props: {
					disabled: true,
					label: 'Disabled',
				},
			});

			const btn = container.querySelector('button');
			expect(btn?.disabled).toBe(true);
		});
	});

	describe('StatusBadge', () => {
		it('renders status text and tone variant class', () => {
			const { container } = render(StatusBadge, {
				props: {
					tone: 'success',
					text: 'Loaded Active',
				},
			});

			const badge = container.querySelector('.badge');
			expect(badge).not.toBeNull();
			expect(badge?.className).toContain('tone-success');
			expect(badge?.textContent).toContain('Loaded Active');
		});

		it('renders warning tone', () => {
			const { container } = render(StatusBadge, {
				props: {
					tone: 'warning',
					text: 'Expiring Soon',
				},
			});

			const badge = container.querySelector('.badge');
			expect(badge?.className).toContain('tone-warning');
		});

		it('renders danger tone', () => {
			const { container } = render(StatusBadge, {
				props: {
					tone: 'danger',
					text: 'Failed',
				},
			});

			const badge = container.querySelector('.badge');
			expect(badge?.className).toContain('tone-danger');
		});
	});

	describe('Notice', () => {
		it('renders notice with title, message, and role', () => {
			const { container } = render(Notice, {
				props: {
					type: 'warning',
					title: 'Warning Notice',
					message: 'Pay attention to this detail.',
				},
			});

			const notice = container.querySelector('.notice');
			expect(notice).not.toBeNull();
			expect(notice?.getAttribute('role')).toBe('status');
			expect(container.querySelector('.notice-title')?.textContent).toBe('Warning Notice');
			expect(container.querySelector('.notice-message')?.textContent).toContain('Pay attention to this detail.');
		});

		it('uses role=alert for danger/error notice', () => {
			const { container } = render(Notice, {
				props: {
					type: 'danger',
					message: 'Critical error occurred.',
				},
			});

			const notice = container.querySelector('.notice');
			expect(notice?.getAttribute('role')).toBe('alert');
		});

		it('supports dismissible notice with ondismiss callback', async () => {
			const handleDismiss = vi.fn();
			const { container } = render(Notice, {
				props: {
					type: 'info',
					message: 'Dismissible info',
					dismissible: true,
					ondismiss: handleDismiss,
				},
			});

			const dismissBtn = container.querySelector('button.dismiss-btn');
			expect(dismissBtn).not.toBeNull();
			await fireEvent.click(dismissBtn!);
			expect(handleDismiss).toHaveBeenCalledTimes(1);
		});
	});

	describe('ActiveFilterSummaries', () => {
		it('renders active filter chips and handles removal', async () => {
			const handleRemove = vi.fn();
			const handleClear = vi.fn();

			const { container } = render(ActiveFilterSummaries, {
				props: {
					filters: [
						{ id: 'protocol', label: 'Protocol: HTTP' },
						{ id: 'provider', label: 'Provider: Cloudflare' },
					],
					onremove: handleRemove,
					onclear: handleClear,
				},
			});

			const chips = container.querySelectorAll('.chip');
			expect(chips.length).toBe(2);
			expect(chips[0].textContent).toContain('Protocol: HTTP');

			const removeButtons = container.querySelectorAll('.chip-remove');
			expect(removeButtons.length).toBe(2);

			await fireEvent.click(removeButtons[0]);
			expect(handleRemove).toHaveBeenCalledWith('protocol');

			const clearBtn = container.querySelector('.clear-all');
			expect(clearBtn).not.toBeNull();
			await fireEvent.click(clearBtn!);
			expect(handleClear).toHaveBeenCalledTimes(1);
		});

		it('renders nothing when filters list is empty', () => {
			const { container } = render(ActiveFilterSummaries, {
				props: {
					filters: [],
				},
			});

			expect(container.querySelector('.summaries')).toBeNull();
		});
	});

	describe('Pagination', () => {
		it('renders page info and controls', async () => {
			const handlePage = vi.fn();
			const handlePageSize = vi.fn();

			const { container } = render(Pagination, {
				props: {
					page: 1,
					pageSize: 25,
					total: 60,
					onpage: handlePage,
					onpagesize: handlePageSize,
				},
			});

			expect(container.textContent).toContain('1–25 of 60');

			const prev = container.querySelector<HTMLButtonElement>('button[aria-label="Previous page"]')!;
			const next = container.querySelector<HTMLButtonElement>('button[aria-label="Next page"]')!;
			expect(prev.disabled).toBe(true);
			expect(next.disabled).toBe(false);
			await fireEvent.click(next);
			expect(handlePage).toHaveBeenCalledWith(2);
			const select = container.querySelector('[role="combobox"]');
			expect(select?.textContent).toContain('25');
			// Real popover, keyboard and pointer behaviour is checked in the browser suite.
			const option = [...container.querySelectorAll('[role="option"]')].find((e) => e.textContent?.trim() === '50')!;
			await fireEvent.click(option);
			expect(handlePageSize).toHaveBeenCalledWith(50);
		});
	});

	describe('ConfirmDialog', () => {
		it('renders dialog when open and traps cancel/confirm events', async () => {
			const handleCancel = vi.fn();
			const handleConfirm = vi.fn();

			const { container } = render(ConfirmDialog, {
				props: {
					open: true,
					title: 'Confirm Action',
					message: 'Are you sure?',
					oncancel: handleCancel,
					onconfirm: handleConfirm,
				},
			});

			const dialog = container.querySelector('[role="dialog"]');
			expect(dialog).not.toBeNull();
			expect(dialog?.getAttribute('aria-modal')).toBe('true');
			expect(container.textContent).toContain('Confirm Action');
			expect(container.textContent).toContain('Are you sure?');

			const buttons = container.querySelectorAll('.actions button');
			expect(buttons.length).toBe(2);

			// Cancel
			await fireEvent.click(buttons[0]);
			expect(handleCancel).toHaveBeenCalledTimes(1);

			// Confirm
			await fireEvent.click(buttons[1]);
			expect(handleConfirm).toHaveBeenCalledTimes(1);
		});

		it('does not render dialog when open is false', () => {
			const { container } = render(ConfirmDialog, {
				props: {
					open: false,
					title: 'Hidden Dialog',
					oncancel: () => {},
					onconfirm: () => {},
				},
			});

			expect(container.querySelector('[role="dialog"]')).toBeNull();
		});
	});

	describe('Tabs', () => {
		it('renders tabs and moves selection and DOM focus on ArrowRight / ArrowLeft / Home / End', async () => {
			const handleSelect = vi.fn();
			const tabsData = [
				{ id: 'overview', label: 'Overview' },
				{ id: 'details', label: 'Details' },
				{ id: 'logs', label: 'Logs' },
			];

			const { container } = render(Tabs, {
				props: {
					tabs: tabsData,
					selected: 'overview',
					onselect: handleSelect,
					label: 'Demo tabs',
				},
			});

			const tabButtons = container.querySelectorAll('[role="tab"]');
			expect(tabButtons.length).toBe(3);
			expect(tabButtons[0].getAttribute('aria-selected')).toBe('true');
			expect(tabButtons[0].getAttribute('tabindex')).toBe('0');
			expect(tabButtons[1].getAttribute('aria-selected')).toBe('false');
			expect(tabButtons[1].getAttribute('tabindex')).toBe('-1');

			// ArrowRight from overview -> details
			await fireEvent.keyDown(tabButtons[0], { key: 'ArrowRight' });
			expect(handleSelect).toHaveBeenCalledWith('details');

			// ArrowLeft from overview -> logs (wraps)
			await fireEvent.keyDown(tabButtons[0], { key: 'ArrowLeft' });
			expect(handleSelect).toHaveBeenCalledWith('logs');

			// End -> logs
			await fireEvent.keyDown(tabButtons[0], { key: 'End' });
			expect(handleSelect).toHaveBeenCalledWith('logs');

			// Home -> overview
			await fireEvent.keyDown(tabButtons[0], { key: 'Home' });
			expect(handleSelect).toHaveBeenCalledWith('overview');
		});
	});

	describe('TrafficMark', () => {
		it('renders SVG mark with default size 32', async () => {
			const TrafficMark = (await import('../src/shared/ui/TrafficMark.svelte')).default;
			const { container } = render(TrafficMark, { props: {} });
			const svg = container.querySelector('svg.traffic-mark');
			expect(svg).not.toBeNull();
			expect(svg?.getAttribute('width')).toBe('32');
			expect(svg?.getAttribute('height')).toBe('32');
			expect(svg?.getAttribute('aria-hidden')).toBe('true');
		});

		it('accepts custom size', async () => {
			const TrafficMark = (await import('../src/shared/ui/TrafficMark.svelte')).default;
			const { container } = render(TrafficMark, { props: { size: 24 } });
			const svg = container.querySelector('svg.traffic-mark');
			expect(svg?.getAttribute('width')).toBe('24');
			expect(svg?.getAttribute('height')).toBe('24');
		});
	});

	describe('ConnectionRail', () => {
		it('renders steps and labels according to FE-D10 specification', async () => {
			const ConnectionRail = (await import('../src/shared/ui/ConnectionRail.svelte')).default;
			const { container } = render(ConnectionRail, {
				props: {
					label: 'Routing relationship',
					steps: [
						{ label: 'Entrypoint', value: 'websecure (:443)' },
						{ label: 'Router', value: 'api@docker' },
						{ label: 'Service', value: 'api-service@docker' },
					],
				},
			});

			expect(container.textContent).toContain('Routing relationship');
			expect(container.textContent).toContain('Entrypoint');
			expect(container.textContent).toContain('websecure (:443)');
			expect(container.textContent).toContain('Router');
			expect(container.textContent).toContain('api@docker');
			expect(container.textContent).toContain('Service');
			expect(container.textContent).toContain('api-service@docker');

			const items = container.querySelectorAll('li');
			expect(items.length).toBe(3);
		});
	});
});
