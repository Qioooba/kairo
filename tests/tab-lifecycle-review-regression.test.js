// tests/tab-lifecycle-review-regression.test.js
// Regression tests for R1, R2, R8, R9 (Tab lifecycle, beforeunload, notes drafts & subroutes)
'use strict';

const fs = require('fs');
const path = require('path');
const vm = require('vm');
const assert = require('assert');

function makeElement(tag) {
  const children = [];
  const classSet = new Set();
  const attrs = {};
  const listeners = {};
  const el = {
    tagName: (tag || 'div').toUpperCase(),
    nodeType: 1,
    children,
    childNodes: children,
    style: {},
    dataset: {},
    get className() { return Array.from(classSet).join(' '); },
    set className(v) {
      classSet.clear();
      String(v || '').split(/\s+/).filter(Boolean).forEach(c => classSet.add(c));
    },
    classList: {
      add: (c) => classSet.add(c),
      remove: (c) => classSet.delete(c),
      toggle: (c, force) => {
        if (force === undefined) {
          if (classSet.has(c)) classSet.delete(c); else classSet.add(c);
        } else if (force) classSet.add(c); else classSet.delete(c);
      },
      contains: (c) => classSet.has(c)
    },
    setAttribute: (k, v) => { attrs[k] = String(v); },
    getAttribute: (k) => attrs[k] || null,
    removeAttribute: (k) => { delete attrs[k]; },
    appendChild: (child) => {
      if (child) {
        child.parentElement = el;
        child.parentNode = el;
        children.push(child);
      }
      return child;
    },
    append: (...items) => {
      items.forEach(child => {
        if (child) {
          child.parentElement = el;
          child.parentNode = el;
          children.push(child);
        }
      });
    },
    removeChild: (child) => {
      const idx = children.indexOf(child);
      if (idx >= 0) children.splice(idx, 1);
      return child;
    },
    addEventListener: (type, fn) => {
      listeners[type] = listeners[type] || [];
      listeners[type].push(fn);
    },
    removeEventListener: (type, fn) => {
      if (listeners[type]) {
        listeners[type] = listeners[type].filter(f => f !== fn);
      }
    },
    dispatchEvent: (ev) => {
      const fns = listeners[ev.type] || [];
      fns.forEach(fn => fn(ev));
      return true;
    },
    querySelector: (sel) => {
      for (const c of children) {
        if (!c) continue;
        if (sel.startsWith('.') && c.classList && c.classList.contains(sel.slice(1))) return c;
        if (sel.startsWith('#') && attrs.id === sel.slice(1)) return c;
        const sub = c.querySelector && c.querySelector(sel);
        if (sub) return sub;
      }
      return null;
    },
    querySelectorAll: (sel) => {
      let res = [];
      for (const c of children) {
        if (!c) continue;
        if (sel.startsWith('.') && c.classList && c.classList.contains(sel.slice(1))) res.push(c);
        if (c.querySelectorAll) res = res.concat(c.querySelectorAll(sel));
      }
      return res;
    },
    get isConnected() { return true; },
    innerHTML: '',
    textContent: '',
    value: '',
    scrollTop: 0
  };
  return el;
}

function createTestEnv(opts = {}) {
  const routeScopeSrc = fs.readFileSync(path.join(__dirname, '../web/workbench/route-scope.js'), 'utf8');
  const coreSrc = fs.readFileSync(path.join(__dirname, '../web/core.js'), 'utf8');
  const tabsSrc = fs.readFileSync(path.join(__dirname, '../web/tabs.js'), 'utf8');
  const appSrc = fs.readFileSync(path.join(__dirname, '../web/app.js'), 'utf8');

  const listeners = {};
  const viewRoot = makeElement('section');
  const barEl = makeElement('div');
  const windowListeners = {};

  let _hash = opts.hash || '#/home';
  const win = {
    addEventListener: (type, fn) => {
      windowListeners[type] = windowListeners[type] || [];
      windowListeners[type].push(fn);
    },
    removeEventListener: (type, fn) => {
      if (windowListeners[type]) {
        windowListeners[type] = windowListeners[type].filter(f => f !== fn);
      }
    },
    dispatchEvent: (ev) => {
      const fns = windowListeners[ev.type] || [];
      fns.forEach(fn => fn(ev));
    },
    document: {
      body: makeElement('body'),
      createElement: (t) => makeElement(t),
      createTextNode: (t) => ({ nodeType: 3, textContent: String(t) }),
      getElementById: (id) => (id === 'view' ? viewRoot : id === 'tab-bar' ? barEl : null),
      querySelector: () => null,
      querySelectorAll: () => []
    },
    confirm: opts.confirm || (() => true),
    history: { replaceState: (s, t, url) => { _hash = url; } },
    console,
    setTimeout, clearTimeout, setInterval, clearInterval,
    AbortController,
    navigator: { sendBeacon: opts.sendBeacon || (() => true) },
    localStorage: {
      _data: {},
      getItem: (k) => win.localStorage._data[k] || null,
      setItem: (k, v) => { win.localStorage._data[k] = String(v); },
      removeItem: (k) => { delete win.localStorage._data[k]; }
    },
    sessionStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    CustomEvent: class CustomEvent {
      constructor(type, eventInitDict) {
        this.type = type;
        this.detail = eventInitDict ? eventInitDict.detail : null;
      }
    },
    Kairo: {
      state: { routes: {}, routeNames: {} },
      pages: {},
      api: { api: async () => ({}) }
    }
  };
  win.location = {
    get hash() { return _hash; },
    set hash(v) {
      _hash = v;
      win.dispatchEvent(new win.CustomEvent('hashchange'));
    }
  };
  win.window = win;

  class Element {}
  class Document {}
  class DocumentFragment {}
  win.Element = Element;
  win.Document = Document;
  win.DocumentFragment = DocumentFragment;

  const ctx = {
    window: win,
    document: win.document,
    Element: Element,
    Document: Document,
    DocumentFragment: DocumentFragment,
    location: win.location,
    history: win.history,
    navigator: win.navigator,
    console,
    setTimeout, clearTimeout, setInterval, clearInterval,
    AbortController,
    localStorage: win.localStorage,
    sessionStorage: win.sessionStorage,
    CustomEvent: win.CustomEvent
  };

  vm.runInNewContext(routeScopeSrc + '\n' + coreSrc + '\n' + tabsSrc + '\n' + appSrc, ctx);
  win.Kairo.tabs.init({ viewRoot, barEl });

  return { win, windowListeners, viewRoot, barEl };
}

async function runTests() {
  console.log('--- Running tab lifecycle review regressions (R1, R2, R8, R9) ---');

  // Test R1: SSH registration ownership and auto-dismiss of home
  {
    console.log('Testing R1: Tab resource ownership...');
    const env = createTestEnv();
    const { win } = env;
    const core = win.Kairo.core;
    const tabs = win.Kairo.tabs;

    let sshCloseCalls = 0;
    const mockSshController = {
      hasActive: () => true,
      closeAll: () => { sshCloseCalls++; }
    };

    win.Kairo.state = win.Kairo.state || { routes: {}, routeNames: {} };
    win.Kairo.state.routes = win.Kairo.state.routes || {};
    win.Kairo.state.routeNames = win.Kairo.state.routeNames || {};

    // Define routes
    win.Kairo.state.routes.home = (pane) => {
      pane.innerHTML = '<div>Home</div>';
    };
    win.Kairo.state.routeNames.home = '首页';

    win.Kairo.state.routes.ssh = (pane, routeState, scope) => {
      pane.innerHTML = '<div>SSH</div>';
      core.setActiveShells(mockSshController);
      return () => {
        core.setActiveShells(null);
      };
    };
    win.Kairo.state.routeNames.ssh = 'SSH 终端';

    // Start at home
    tabs.openRoute('home');
    assert.strictEqual(tabs.getActiveId(), 'home');

    // Open SSH from home: home should be automatically dismissed, and SSH controller must NOT be closed!
    tabs.openRoute('ssh');
    assert.strictEqual(tabs.getActiveId(), 'ssh');
    assert.strictEqual(sshCloseCalls, 0, 'Opening SSH from home and auto-dismissing home must NOT close SSH controller');
    assert.ok(core.hasActiveShellsFor('ssh'), 'SSH controller must be registered under ssh tab');
    assert.strictEqual(core.hasActiveShellsFor('home'), false, 'Home tab must not own SSH controller');

    // Test R1.b: Stale unmount of re-opened tab does not wipe new tab's resource
    let sshCloseCalls2 = 0;
    const mockSshController2 = {
      hasActive: () => true,
      closeAll: () => { sshCloseCalls2++; }
    };
    // Register controller 2 under ssh
    core.setActiveShells(mockSshController2, 'ssh');
    // Stale cleanup of old controller 1 trying to unregister
    core.setActiveShells(null, 'ssh', mockSshController);
    assert.strictEqual(core.getActiveShells('ssh'), mockSshController2, 'Stale cleanup must NOT delete new instance');

    console.log('  ✓ R1 Tab resource ownership verified');
  }

  // Test R2: beforeunload prompt vs cancellation and pagehide release
  {
    console.log('Testing R2: beforeunload does not destroy resources on cancel...');
    const env = createTestEnv();
    const { win, windowListeners } = env;
    const core = win.Kairo.core;
    const tabs = win.Kairo.tabs;

    let shellClosed = 0;
    let uploadCanceled = 0;

    win.Kairo.state = win.Kairo.state || { routes: {}, routeNames: {} };
    win.Kairo.state.routes.ssh = () => {};
    win.Kairo.state.routeNames.ssh = 'SSH';
    tabs.openRoute('ssh');

    core.setActiveShells({
      hasActive: () => true,
      closeAll: () => { shellClosed++; }
    }, 'ssh');

    core.setActiveUploads({
      hasActive: () => true,
      cancelAll: () => { uploadCanceled++; },
      cancelAllBeacon: () => { uploadCanceled++; }
    }, 'ssh');

    // Simulate beforeunload event
    let prevented = false;
    let returnValue = null;
    const beforeUnloadEv = {
      preventDefault: () => { prevented = true; },
      set returnValue(val) { returnValue = val; },
      get returnValue() { return returnValue; }
    };

    const beforeUnloadHandlers = windowListeners['beforeunload'] || [];
    assert.ok(beforeUnloadHandlers.length > 0, 'beforeunload handler must be registered');

    beforeUnloadHandlers.forEach(fn => fn(beforeUnloadEv));

    assert.ok(prevented || returnValue, 'beforeunload must warn user of unsaved/active work');
    assert.strictEqual(shellClosed, 0, 'Triggering beforeunload prompt must NOT destroy shells before user decision');
    assert.strictEqual(uploadCanceled, 0, 'Triggering beforeunload prompt must NOT cancel uploads before user decision');

    // Simulate pagehide with persisted: true (bfcache)
    const pageHideHandlers = windowListeners['pagehide'] || [];
    pageHideHandlers.forEach(fn => fn({ persisted: true }));
    assert.strictEqual(shellClosed, 0, 'bfcache pagehide must not destroy resources');

    // Simulate pagehide with persisted: false (real unload)
    pageHideHandlers.forEach(fn => fn({ persisted: false }));
    assert.ok(shellClosed > 0, 'Real pagehide unload must release resources');
    assert.ok(uploadCanceled > 0, 'Real pagehide unload must cancel uploads');

    console.log('  ✓ R2 beforeunload safety verified');
  }

  // Test R8: Notes inline draft Tab close guard
  {
    console.log('Testing R8: Notes inline draft close guard...');
    const notesSrc = fs.readFileSync(path.join(__dirname, '../web/notes.js'), 'utf8');
    const notesPageSrc = fs.readFileSync(path.join(__dirname, '../web/pages/notes.js'), 'utf8');

    let confirmCalled = false;
    let confirmAnswer = false; // User cancels close

    const env = createTestEnv({
      confirm: () => { confirmCalled = true; return confirmAnswer; }
    });
    const { win } = env;

    // Load notes modules into context
    vm.runInNewContext(notesSrc + '\n' + notesPageSrc, env.win);

    // Register an extra route so notes is not the only tab (requestClose refuses to close the only tab)
    win.Kairo.state.routes.files = (p) => { p.innerHTML = 'Files'; };
    win.Kairo.state.routeNames.files = '文件';

    win.Kairo.tabs.openRoute('notes');
    win.Kairo.tabs.openRoute('files');
    win.Kairo.tabs.activate('notes');
    assert.strictEqual(win.Kairo.tabs.getActiveId(), 'notes');
    assert.ok(win.Kairo.tabs.getOpenIds().length > 1, 'Multiple tabs must be open');

    // Start inline editing
    win.Kairo.notes.setInlineDraft('n1', true);
    assert.strictEqual(win.Kairo.notes.hasUnsaved(), true);

    // Try closing notes tab with cancel confirm
    confirmCalled = false;
    confirmAnswer = false;
    win.Kairo.tabs.requestClose('notes');

    assert.ok(confirmCalled, 'Must trigger confirmation guard when closing notes with inline drafts');
    assert.ok(win.Kairo.tabs.hasTab('notes'), 'Notes tab must remain open when user cancels confirmation');
    assert.strictEqual(win.Kairo.notes.state.inlineDrafts['n1'], true, 'Draft must be preserved');

    // Try closing notes tab with confirm (user accepts discard)
    confirmCalled = false;
    confirmAnswer = true;
    win.Kairo.tabs.requestClose('notes');
    assert.strictEqual(win.Kairo.tabs.hasTab('notes'), false, 'Accepting discard closes notes tab');
    assert.strictEqual(win.Kairo.notes.state.inlineDrafts['n1'], undefined, 'Discard must clear inline draft for closed page');

    console.log('  ✓ R8 Notes inline draft close guard verified');
  }

  // Test R9: Notes subroute synchronization
  {
    console.log('Testing R9: Notes subroute synchronization...');
    const notesSrc = fs.readFileSync(path.join(__dirname, '../web/notes.js'), 'utf8');
    const notesPageSrc = fs.readFileSync(path.join(__dirname, '../web/pages/notes.js'), 'utf8');

    const env = createTestEnv();
    const { win } = env;

    // Mock reminders
    let reminderEditorOpened = false;
    win.Kairo.reminders = {
      render: () => {},
      openEditor: (id, freq, cb, opts) => {
        reminderEditorOpened = true;
      }
    };

    vm.runInNewContext(notesSrc + '\n' + notesPageSrc, env.win);

    // 1. Open notes at list
    win.Kairo.tabs.openRoute('notes', { tab: 'list' });
    assert.strictEqual(win.Kairo.tabs.getActiveId(), 'notes');
    assert.strictEqual(win.location.hash, '#/notes/list');

    // 2. While notes is kept alive, call openReminder
    win.Kairo.notes.openReminder({ id: 'n1', title: 'Task', body: 'Body' });

    // Hash should change to #/notes/reminders, triggering navigate -> openRoute
    win.Kairo.tabs.openRoute('notes', { tab: 'reminders' });

    assert.strictEqual(win.location.hash, '#/notes/reminders');
    const pane = win.Kairo.tabs.getActive().pane;
    const tabBtns = pane.querySelectorAll('.tab-btn');
    const reminderTabBtn = tabBtns[1];
    assert.ok(reminderTabBtn && reminderTabBtn.classList.contains('active'), 'Reminders tab button must become active');
    assert.strictEqual(win.Kairo.notes.state.pendingReminder, null, 'pendingReminder must be consumed');

    console.log('  ✓ R9 Notes subroute synchronization verified');
  }

  console.log('All tab lifecycle review regressions passed!');
}

runTests().catch(err => {
  console.error('Test failed:', err);
  process.exit(1);
});
