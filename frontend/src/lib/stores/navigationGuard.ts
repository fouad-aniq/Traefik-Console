// Navigation guard store for unsaved changes

type PendingAction = (() => void) | null;

class NavigationGuardStore {
  private _isDirty = false;
  private _confirmPending = false;
  private _pendingAction: PendingAction = null;
  private _discardHandlers = new Set<() => void>();

  onDiscard(handler: () => void) {
    this._discardHandlers.add(handler);
    return () => { this._discardHandlers.delete(handler); };
  }

  private _listeners: Set<() => void> = new Set();

  get isDirty() {
    return this._isDirty;
  }

  get confirmPending() {
    return this._confirmPending;
  }

  subscribe(listener: () => void) {
    this._listeners.add(listener);
    listener();
    return () => {
      this._listeners.delete(listener);
    };
  }

  private notify() {
    for (const listener of this._listeners) {
      listener();
    }
  }

  setDirty(dirty: boolean) {
    this._isDirty = dirty;
    this.notify();
  }

  requestNavigation(action: () => void): boolean {
    if (!this._isDirty) {
      action();
      return true;
    }
    this._pendingAction = action;
    this._confirmPending = true;
    this.notify();
    return false;
  }

  confirmDiscard() {
    for (const handler of this._discardHandlers) handler();
    this._isDirty = false;
    this._confirmPending = false;
    const act = this._pendingAction;
    this._pendingAction = null;
    this.notify();
    if (act) {
      act();
    }
  }

  cancelDiscard() {
    this._confirmPending = false;
    this._pendingAction = null;
    this.notify();
  }
}

export const navigationGuard = new NavigationGuardStore();
