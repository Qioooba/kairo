/* ===== web/pages/cron.js =====
 * Cron 表达式解析：单输入框 + 「解析」+ 展示未来 5 次 / 过去 3 次运行时间。
 *
 * 支持 5 段（标准）和 6 段（带秒）以及 @every / @hourly / @daily 等描述符。
 * 解析由后端 robfig/cron/v3 完成。
 *
 * 左侧：表达式 + 时区 + 字段含义 + 描述符说明
 * 右侧：下次运行 5 次（高亮最近一次）+ 上次运行 3 次
 */

(function () {
  'use strict';
  const OTB = window.OTB = window.OTB || {};
  OTB.pages = OTB.pages || {};
  const { el, toast, copyToClipboard } = OTB.core;
  const { api } = OTB.api;

  const COMMON_TZ = [
    'Local', 'UTC',
    'Asia/Shanghai', 'Asia/Tokyo', 'Asia/Singapore', 'Asia/Hong_Kong',
    'Europe/London', 'Europe/Paris', 'Europe/Berlin', 'Europe/Moscow',
    'America/New_York', 'America/Chicago', 'America/Los_Angeles',
    'Australia/Sydney', 'Pacific/Auckland',
  ];

  const SAMPLES = [
    { label: '每分钟', expr: '* * * * *' },
    { label: '每小时', expr: '@hourly' },
    { label: '每天 0 点', expr: '0 0 * * *' },
    { label: '每天 9 点', expr: '0 9 * * *' },
    { label: '工作日 9 点', expr: '0 9 * * 1-5' },
    { label: '每周一 8:30', expr: '30 8 * * 1' },
    { label: '每月 1 号 0 点', expr: '@monthly' },
    { label: '每 5 分钟', expr: '*/5 * * * *' },
    { label: '每 30 秒', expr: '*/30 * * * * *' },
    { label: '每 5 秒', expr: '@every 5s' },
    { label: '每 1 小时 30 分', expr: '@every 1h30m' },
  ];

  function renderCron(view) {
    const inInp = el('input', { type: 'text', id: 'cron-in', placeholder: 'Cron 表达式，如 0 9 * * 1-5 / @hourly / */5 * * * *' });
    inInp.value = '0 9 * * *';
    inInp.style.fontFamily = 'ui-monospace, SFMono-Regular, "Cascadia Mono", Menlo, Consolas, monospace';
    inInp.style.fontSize = '14px';

    const locSel = el('select', { id: 'cron-loc' });
    COMMON_TZ.forEach(tz => locSel.appendChild(el('option', { value: tz, text: tz })));
    locSel.value = 'Local';
    locSel.style.width = '180px';

    // 状态条
    const statusTag = el('span', { class: 'tag', text: '—' });
    statusTag.style.visibility = 'hidden'; // 初始空状态，doParse 后才显示
    const secTag = el('span', { class: 'tag', text: '' });
    secTag.style.marginLeft = '4px';
    secTag.style.visibility = 'hidden'; // 初始空状态

    // 字段说明
    const descBox = el('div', { class: 'cron-desc', text: '点"解析"后展示字段含义' });

    // 下次 / 上次运行列表
    const nextList = el('div', { class: 'cron-list' });
    const prevList = el('div', { class: 'cron-list' });

    function renderRuns(list, runs, kind) {
      list.innerHTML = '';
      if (!runs || runs.length === 0) {
        list.appendChild(el('div', { class: 'muted', text: '（无）' }));
        return;
      }
      runs.forEach((r, i) => {
        const row = el('div', { class: 'cron-run-row' + (kind === 'next' && i === 0 ? ' first' : '') }, [
          el('span', { class: 'cron-idx', text: kind === 'next' ? '→ ' + (i + 1) : '← ' + (i + 1) }),
          el('span', { class: 'cron-cn', text: r.cn || '' }),
          el('span', { class: 'cron-unix muted', text: 'Unix ' + r.unix }),
          el('button', { class: 'btn btn-sm', text: '复制', onclick: () => {
            copyToClipboard(r.cn || '').then(() => toast('已复制', 'ok'), () => toast('复制失败', 'err'));
          }}),
        ]);
        list.appendChild(row);
      });
    }

    let reqId = 0;
    async function doParse() {
      const input = inInp.value.trim();
      if (!input) { toast('表达式不能为空', 'warn'); return; }
      const myReqId = ++reqId;
      try {
        const r = await api('POST', '/api/format/cron-parse', {
          input,
          loc: locSel.value,
        });
        if (myReqId !== reqId) return; // 旧请求被新请求覆盖，丢弃
        const res = r.res || {};
        if (res.valid) {
          statusTag.textContent = '✓ 合法';
          statusTag.className = 'tag tag-ok';
        } else {
          statusTag.textContent = '✗ 不合法';
          statusTag.className = 'tag tag-err';
        }
        statusTag.style.visibility = '';
        if (res.has_seconds) {
          secTag.textContent = '6 段（带秒）';
          secTag.className = 'tag tag-warn';
        } else {
          secTag.textContent = '5 段';
          secTag.className = 'tag';
        }
        secTag.style.visibility = '';
        descBox.textContent = res.field_desc || '';
        renderRuns(nextList, res.next_runs, 'next');
        renderRuns(prevList, res.prev_runs, 'prev');
        if (!res.valid && res.error) {
          toast('解析失败：' + res.error, 'err');
        }
      } catch (e) {
        if (myReqId !== reqId) return; // 旧请求，丢弃错误提示
        toast('解析失败：' + (e.message || e), 'err');
      }
    }

    const btnParse = el('button', { class: 'btn btn-primary', text: '解析', onclick: doParse });
    const btnClear = el('button', { class: 'btn', text: '清空', onclick: () => {
      inInp.value = '';
      statusTag.textContent = '—'; statusTag.className = 'tag'; statusTag.style.visibility = 'hidden';
      secTag.textContent = ''; secTag.className = 'tag'; secTag.style.visibility = 'hidden';
      descBox.textContent = '点"解析"后展示字段含义';
      nextList.innerHTML = ''; prevList.innerHTML = '';
      reqId++;
    }});

    // 样例按钮
    const sampleBox = el('div', { class: 'cron-samples' });
    SAMPLES.forEach(s => {
      const b = el('button', { class: 'btn btn-sm', text: s.label, onclick: () => {
        inInp.value = s.expr;
        doParse();
      }});
      sampleBox.appendChild(b);
    });

    inInp.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); doParse(); }
    });
    locSel.addEventListener('change', doParse);

    view.appendChild(el('div', { class: 'card' }, [
      el('div', { class: 'card-desc', text: '解析 5 段 / 6 段 / @ 描述符的 cron 表达式，展示未来 5 次 + 过去 3 次运行时间，按所选时区显示。' }),
      el('div', { class: 'cron-row' }, [
        inInp,
        el('label', { class: 'cmp-opt', style: 'flex:0 0 auto' }, [locSel, el('span', { text: ' 时区' })]),
        btnParse, btnClear,
        statusTag, secTag,
      ]),
      el('div', { class: 'cron-samples-wrap' }, [
        el('label', { text: '常用样例：' }),
        sampleBox,
      ]),
      el('div', { class: 'cron-grid' }, [
        el('div', { class: 'cron-col' }, [
          el('h4', { text: '字段含义' }),
          descBox,
        ]),
        el('div', { class: 'cron-col' }, [
          el('h4', { text: '下次 5 次' }),
          nextList,
        ]),
        el('div', { class: 'cron-col' }, [
          el('h4', { text: '上次 3 次' }),
          prevList,
        ]),
      ]),
    ]));
  }

  OTB.pages.cron = renderCron;
  OTB.state.routes.cron = renderCron;
  OTB.state.routeNames.cron = 'Cron 解析';
  OTB.state.routeSubs.cron = '表达式 / 描述符 / 5+3 次运行';
})();
