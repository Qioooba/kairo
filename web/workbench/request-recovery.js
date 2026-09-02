/* Request recovery policy.  The backend marks a request retryable; the
 * browser only performs the one retry explicitly requested by that contract. */
(function () {
  'use strict';
  const K = window.Kairo = window.Kairo || {};
  const W = K.workbench = K.workbench || {};
  function retryable(error) {
    return !!(error && error.data && error.data.retryable && !error.data.output_started);
  }
  async function once(fn, onRetry) {
    try { return await fn(0); } catch (error) {
      if (!retryable(error)) throw error;
      if (typeof onRetry === 'function') onRetry(error);
      return fn(1);
    }
  }
  W.requestRecovery = { retryable: retryable, once: once };
})();
