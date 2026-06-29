/* ===== web/pages/timestamp.js =====
 * 时间戳转换：单输入框 + 「现在」「↔」「复制」一行按钮。
 *
 * 智能识别输入类型：
 *   - "now"（不区分大小写）→ 当前时间
 *   - 10 位纯数字 → Unix 秒
 *   - 13 位纯数字 → Unix 毫秒
 *   - 含 T 且带时区/Z → ISO 8601
 *   - 其它（2024-06-24 13:20:00、2024/06/24、06-24-2024 ...） → 人类可读
 *
 * from_tz / to_tz 是 IANA 时区名（UTC / Local / Asia/Shanghai / ...）。
 * - 输入是 Unix 数字或 now：from_tz 决定那一刻在哪个时区
 * - 输入是已带时区的字符串：from_tz 被忽略
 *
 * 输出 8 行（每行一个：UTC / 本机 / 目标时区 / 秒 / 毫秒 / 日期 / 时间 / 周几），
 * 点行复制。
 */

(function () {
  'use strict';
  const DTB = window.DTB = window.DTB || {};
  DTB.pages = DTB.pages || {};
  const { el, toast, copyToClipboard } = DTB.core;
  const { api } = DTB.api;

  // 常用 IANA 时区（前端下拉够用）
  const COMMON_TZ = [
    'UTC', 'Local',
    'Asia/Shanghai', 'Asia/Tokyo', 'Asia/Singapore', 'Asia/Hong_Kong', 'Asia/Kolkata',
    'Europe/London', 'Europe/Paris', 'Europe/Berlin', 'Europe/Moscow',
    'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles', 'America/Sao_Paulo',
    'Australia/Sydney', 'Pacific/Auckland',
  ];

  function renderTimestamp(view) {
    const inInp = el('input', { type: 'text', id: 'ts-in', placeholder: '输入：1719240000 / 1719240000000 / 2024-06-24 13:20:00 / 2024-06-24T13:20:00Z / now' });
    inInp.style.fontSize = '15px';

    const fromSel = el('select', { id: 'ts-from' });
    const toSel = el('select', { id: 'ts-to' });
    COMMON_TZ.forEach(tz => {
      fromSel.appendChild(el('option', { value: tz, text: tz }));
      toSel.appendChild(el('option', { value: tz, text: tz }));
    });
    fromSel.value = 'UTC';
    toSel.value = 'Local';
    fromSel.style.width = '180px';
    toSel.style.width = '180px';

    // 输出区：8 行键值对 + 复制按钮
    const outBox = el('div', { class: 'ts-out' });
    const outRows = {};
    const rowLabels = [
      { key: 'input_type', label: '输入类型' },
      { key: 'utc', label: 'UTC（RFC 3339）' },
      { key: 'local', label: '本机时区' },
      { key: 'target', label: '目标时区' },
      { key: 'unix_sec', label: 'Unix 秒' },
      { key: 'unix_milli', label: 'Unix 毫秒' },
      { key: 'date_cn', label: '日期（人类）' },
      { key: 'weekday_cn', label: '星期' },
    ];
    rowLabels.forEach(r => {
      const v = el('span', { class: 'ts-v', text: '—' });
      outRows[r.key] = v;
      const copyBtn = el('button', { class: 'btn btn-sm', text: '复制', onclick: () => {
        if (!v.textContent || v.textContent === '—') return;
        copyToClipboard(v.textContent).then(
          () => toast('已复制', 'ok'),
          () => toast('复制失败', 'err')
        );
      }});
      outBox.appendChild(el('div', { class: 'ts-row' }, [
        el('span', { class: 'ts-k', text: r.label }),
        v,
        copyBtn,
      ]));
    });

    function fillOut(r) {
      outRows.input_type.textContent = r.input_type || '—';
      outRows.utc.textContent = r.utc || '—';
      outRows.local.textContent = r.local || '—';
      outRows.target.textContent = r.target || '—';
      outRows.unix_sec.textContent = r.unix_sec != null ? String(r.unix_sec) : '—';
      outRows.unix_milli.textContent = r.unix_milli != null ? String(r.unix_milli) : '—';
      outRows.date_cn.textContent = r.year
        ? (r.year + '年' + r.month + '月' + r.day + '日 ' +
           String(r.hour).padStart(2, '0') + ':' + String(r.minute).padStart(2, '0') + ':' + String(r.second).padStart(2, '0'))
        : '—';
      outRows.weekday_cn.textContent = r.weekday_cn || '—';
    }

    let reqId = 0;
    async function doConvert() {
      const input = inInp.value.trim();
      if (!input) { toast('输入不能为空', 'warn'); return; }
      const myReqId = ++reqId;
      try {
        const r = await api('POST', '/api/format/timestamp', {
          input,
          from_tz: fromSel.value,
          to_tz: toSel.value,
        });
        if (myReqId !== reqId) return; // 旧请求被新请求覆盖，丢弃
        // 即使 r.res.error 也要 fillOut（把输出区刷新成 invalid 状态），
        // 否则 DOM 残留上次结果
        fillOut(r.res || {});
        if (r.res && r.res.error) {
          toast(r.res.error, 'err');
        }
      } catch (e) {
        if (myReqId !== reqId) return; // 旧请求，丢弃错误提示
        toast('转换失败：' + (e.message || e), 'err');
      }
    }

    const btnNow = el('button', { class: 'btn', text: '现在', onclick: () => { inInp.value = 'now'; doConvert(); }});
    const btnConvert = el('button', { class: 'btn btn-primary', text: '转换', onclick: doConvert });
    const btnSwap = el('button', { class: 'btn', text: '↔  互换时区', onclick: () => {
      const a = fromSel.value, b = toSel.value;
      fromSel.value = b; toSel.value = a;
      doConvert();
    }});
    const btnCopyAll = el('button', { class: 'btn', text: '复制全部（CSV）', onclick: () => {
      const lines = rowLabels.map(r => r.label + ',' + (outRows[r.key].textContent || '')).join('\n');
      copyToClipboard(lines).then(
        () => toast('已复制 ' + rowLabels.length + ' 行', 'ok'),
        () => toast('复制失败', 'err')
      );
    }});

    inInp.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); doConvert(); }
    });
    fromSel.addEventListener('change', doConvert);
    toSel.addEventListener('change', doConvert);

    view.appendChild(el('div', { class: 'card' }, [
      el('div', { class: 'card-desc', text: '智能识别 Unix 秒/毫秒 / ISO 8601 / 人类可读 / now。from 时区 = 输入所在时区（仅 Unix 数字 / now 时生效），to 时区 = 展示时区。' }),
      el('div', { class: 'ts-row-input' }, [
        inInp,
        btnNow, btnConvert, btnSwap,
      ]),
      el('div', { class: 'ts-row-tz' }, [
        el('label', { text: 'from 时区（输入所在）' }), fromSel,
        el('label', { text: 'to 时区（展示）' }), toSel,
        btnCopyAll,
      ]),
      outBox,
      el('div', { class: 'muted cmp-v2-hint', text: '提示：粘贴一段 ISO 字符串会自动覆盖 from 时区（按字符串里的时区解释）；粘贴 Unix 数字时 from 必须正确，否则换算到 to 时区会偏。' }),
    ]));
  }

  DTB.pages.timestamp = renderTimestamp;
  DTB.state.routes.timestamp = renderTimestamp;
  DTB.state.routeNames.timestamp = '时间戳';
  DTB.state.routeSubs.timestamp = 'Unix / ISO / 人类可读 互转';
})();
