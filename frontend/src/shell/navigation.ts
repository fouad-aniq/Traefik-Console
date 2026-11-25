import { navigationGuard } from '../lib/stores/navigationGuard';

const INDEX = 'tcNavigationIndex';

/** Hash routes and browser history. Unsaved-form protection belongs to the shared guard. */
export function createNavigation(onRoute: (path: string) => void) {
 let currentPath = window.location.hash.slice(1) || '/';
 let currentIndex = 0;
 let restoring: { index: number; delta: number } | null = null;
 let approvedIndex: number | null = null;
 const stateAt = (index: number) => ({ ...history.state, [INDEX]: index });

 function navigate(path: string) {
  if (path === currentPath) return;
  navigationGuard.requestNavigation(() => {
   history.pushState(stateAt(++currentIndex), '', '#' + path);
   currentPath = path;
   onRoute(path);
  });
 }

 function changed() {
  const path = window.location.hash.slice(1) || '/';
  const knownIndex = history.state?.[INDEX];
  if (restoring) {
   if (knownIndex !== currentIndex || path !== currentPath) return;
   const target = restoring;
   restoring = null;
   navigationGuard.requestNavigation(() => {
    approvedIndex = target.index;
    history.go(target.delta);
   });
   return;
  }
  if (path === currentPath && knownIndex === currentIndex) return;
  // Direct edits to the hash create an entry that inherits the old state.
  const targetIndex = typeof knownIndex === 'number' && knownIndex !== currentIndex
   ? knownIndex : currentIndex + 1;
  if (knownIndex !== targetIndex) history.replaceState(stateAt(targetIndex), '');
  if (approvedIndex === targetIndex || !navigationGuard.isDirty) {
   approvedIndex = null;
   currentIndex = targetIndex;
   currentPath = path;
   onRoute(path);
   return;
  }
  const delta = targetIndex - currentIndex;
  restoring = { index: targetIndex, delta };
  // Restore the cursor, preserving history. Discard replays the traversal once;
  // Keep editing stays at the restored entry without pushing a duplicate.
  history.go(-delta);
 }

 function beforeUnload(event: BeforeUnloadEvent) {
  if (navigationGuard.isDirty) {
   event.preventDefault();
   event.returnValue = '';
  }
 }

 function start() {
  currentIndex = typeof history.state?.[INDEX] === 'number' ? history.state[INDEX] : 0;
  history.replaceState(stateAt(currentIndex), '');
  window.addEventListener('popstate', changed);
  window.addEventListener('hashchange', changed);
  window.addEventListener('beforeunload', beforeUnload);
  return () => {
   window.removeEventListener('popstate', changed);
   window.removeEventListener('hashchange', changed);
   window.removeEventListener('beforeunload', beforeUnload);
   navigationGuard.cancelDiscard();
  };
 }
 return { navigate, start };
}
