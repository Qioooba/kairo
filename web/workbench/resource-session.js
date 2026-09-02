/* Explicit resource identity for long-lived tabs.  A missing source is an
 * orphan, never a reason to silently execute against the global source. */
(function () {
  'use strict';
  const K = window.Kairo = window.Kairo || {};
  const W = K.workbench = K.workbench || {};

  function snapshot(source) {
    if (!source) return { id: '', name: '', kind: '', version: '' };
    // updated_at is an RFC3339 string in the API.  Converting it with Number()
    // collapses every source to version 0 and makes edits invisible to tabs.
    const version = String(source.version || source.updated_at || '');
    return { id: String(source.id || ''), name: String(source.name || ''), kind: String(source.kind || ''), version: version };
  }
  function resolve(ref, sources) {
    ref = ref || {};
    const id = String(ref.id || ref.sourceId || '');
    const found = (sources || []).find(function (s) { return String(s.id) === id; });
    if (!id || !found) return { status: 'removed', source: null, ref: snapshot(ref) };
    const expected = String(ref.version || '');
    const current = String(found.version || found.updated_at || '');
    return { status: expected && current && expected !== current ? 'changed' : 'ready', source: found, ref: snapshot(found) };
  }
  W.resourceSession = { snapshot: snapshot, resolve: resolve };
})();
