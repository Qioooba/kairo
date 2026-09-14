/* Database object designer and Oracle Function editor.
 *
 * The page-level database renderer intentionally remains unchanged.  This
 * module decorates the existing object viewer and emits stable contracts:
 *   kairo:database-ddl, kairo:database-function-compile,
 *   kairo:database-function-source.
 * A deployment can install adapters with Kairo.databaseFeatures.setAdapter()
 * when the corresponding server endpoints are available.
 */
(function () {
  'use strict';

  const K = window.Kairo = window.Kairo || {};
  const F = K.databaseFeatures = K.databaseFeatures || {};
  const D = K.databaseDesigner = K.databaseDesigner || {};
  const state = { started: false, observer: null, viewer: null, current: null, sourceCache: Object.create(null) };

  function esc(value) {
    const fn = K.core && K.core.escapeHtml;
    if (fn) return fn(String(value == null ? '' : value));
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) { return ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]; });
  }
  function id(name) { return document.getElementById(name); }
  function toast(value, type) { if (K.core && K.core.toast) K.core.toast(String(value), type || 'info'); }
  function sourceId() { const select = id('db-source'); return select && select.value ? String(select.value) : ''; }
  function schemaName() { const select = id('db-schema'); return select && select.value ? String(select.value) : ''; }
  function api(method, path, body) {
    if (K.api && K.api.api) return K.api.api(method, path, body);
    return fetch(path, { method: method, credentials: 'same-origin', headers: body === undefined ? {} : { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) }).then(function (response) { return response.json().catch(function () { return {}; }).then(function (data) { if (!response.ok) { const error = new Error(data.error || 'HTTP ' + response.status); error.status = response.status; error.data = data; throw error; } return data; }); });
  }
  function emit(type, detail) {
    let event;
    try { event = new CustomEvent(type, { detail: detail }); } catch (_) { event = document.createEvent('CustomEvent'); event.initCustomEvent(type, false, false, detail); }
    window.dispatchEvent(event); return event;
  }
  function createModal(options) {
    if (F.createModal) return F.createModal(options);
    const wrapper = document.createElement('div'); wrapper.className = 'db-pro-modal-overlay';
    wrapper.innerHTML = '<section class="db-pro-modal" role="dialog" aria-modal="true"><header class="db-pro-modal-head"><h2>' + esc(options.title || '数据库对象') + '</h2><button type="button" class="db-pro-modal-close" aria-label="关闭">×</button></header><div class="db-pro-modal-body">' + String(options.body || '') + '</div><footer class="db-pro-modal-foot"></footer></section>';
    const card = wrapper.querySelector('.db-pro-modal'), foot = wrapper.querySelector('.db-pro-modal-foot');
    (options.actions || [{ text: '关闭' }]).forEach(function (action) { const button = document.createElement('button'); button.type = 'button'; button.className = action.className || 'btn'; button.textContent = action.text; if (action.id) button.id = action.id; button.onclick = function () { if (action.onClick) action.onClick(button, { body: card.querySelector('.db-pro-modal-body'), card: card }); if (action.close !== false) wrapper.remove(); }; foot.appendChild(button); });
    wrapper.querySelector('.db-pro-modal-close').onclick = function () { wrapper.remove(); };
    document.body.appendChild(wrapper);
    return { overlay: wrapper, card: card, body: card.querySelector('.db-pro-modal-body'), close: function () { wrapper.remove(); } };
  }
  function currentObject(viewer) {
    const title = viewer.querySelector('.db-obj-header h3'), kind = viewer.querySelector('.db-obj-kind-tag');
    if (!title) return null;
    const full = title.textContent.trim(), parts = full.split('.');
    return { schema: parts.length > 1 ? parts.slice(0, -1).join('.') : schemaName(), object: parts[parts.length - 1], type: (kind ? kind.textContent : '').trim().toUpperCase() || 'TABLE' };
  }
  function parseFields(viewer) {
    const result = [];
    // The object viewer renders separate tables for fields, indexes and
    // constraints.  Never infer a field from whichever tab happens to be
    // visible: an index row has the same minimum cell count but a completely
    // different meaning.  If the older markup has no field header, return an
    // empty draft and let the typed structure endpoint fill it.
    const tables = Array.from(viewer.querySelectorAll('.db-obj-table'));
    const fieldTable = tables.find(function (table) {
      const headers = Array.from(table.querySelectorAll('thead th')).map(function (cell) { return cell.textContent.trim(); }).join(' ');
      return /字段名/.test(headers) && /数据类型/.test(headers);
    });
    if (!fieldTable) return result;
    fieldTable.querySelectorAll('tbody tr').forEach(function (row) {
      const cells = row.querySelectorAll('td'); if (cells.length < 3) return;
      const values = Array.from(cells).map(function (cell) { return cell.textContent.trim(); });
      if (!values[1] || values[1] === '-') return;
      result.push({ name: values[1], type: values[2] || 'VARCHAR2(255)', primary: /\bPK\b/i.test(values[3] || ''), nullable: !/NOT NULL/i.test(values[4] || ''), defaultValue: values[5] === '-' ? '' : values[5] });
    });
    return result;
  }
  function sameIndex(a, b) {
    if (!a || !b) return false;
    const columns = function (value) { return String(value || '').split(',').map(function (item) { return item.trim().toUpperCase(); }).filter(Boolean).join(','); };
    return String(a.name || '').toUpperCase() === String(b.name || '').toUpperCase()
      && String(a.table || '').toUpperCase() === String(b.table || '').toUpperCase()
      && columns(a.columns) === columns(b.columns)
      && !!a.unique === !!b.unique
      // The table-level editor currently does not expose an index-method
      // control.  An omitted current value therefore means "preserve the
      // server method", not "change it to empty".
      && (!String(b.method || '').trim() || String(a.method || '').toUpperCase() === String(b.method || '').toUpperCase());
  }
  function sameConstraint(a, b) {
    if (!a || !b) return false;
    const list = function (value) { return String(value || '').split(',').map(function (item) { return item.trim().toUpperCase(); }).filter(Boolean).join(','); };
    return String(a.name || '').toUpperCase() === String(b.name || '').toUpperCase()
      && String(a.table || '').toUpperCase() === String(b.table || '').toUpperCase()
      && String(a.type || '').toUpperCase() === String(b.type || '').toUpperCase()
      && list(a.columns) === list(b.columns)
      && String(a.expression || '').trim() === String(b.expression || '').trim()
      && String(a.referencedSchema || '').toUpperCase() === String(b.referencedSchema || '').toUpperCase()
      && String(a.referencedTable || '').toUpperCase() === String(b.referencedTable || '').toUpperCase()
      && list(a.referencedColumns) === list(b.referencedColumns)
      && String(a.onDelete || '').toUpperCase() === String(b.onDelete || '').toUpperCase();
  }
  function quoteIdentifier(value) {
    const text = String(value || '').trim();
    if (!text) return '';
    if (/^[A-Za-z_$#][A-Za-z0-9_$#]*$/.test(text)) return text;
    return '"' + text.replace(/"/g, '""') + '"';
  }
  function objectName(context) { return (context.schema ? quoteIdentifier(context.schema) + '.' : '') + quoteIdentifier(context.object); }
  function sourceKind() {
    const badge = id('db-source-badge'), text = badge && badge.textContent ? badge.textContent.toLowerCase() : '';
    return text.indexOf('mysql') >= 0 ? 'mysql' : 'oracle';
  }
  function typeLabel(type) { return ({ TABLE: '表', VIEW: '视图', INDEX: '索引', CONSTRAINT: '约束', SEQUENCE: '序列' })[String(type || '').toUpperCase()] || type; }
  function cloneFields(fields) { return (fields || []).map(function (field) { return Object.assign({}, field); }); }
  function renderFieldRows(list, fields) {
    list.innerHTML = fields.map(function (field, index) { return '<div class="db-pro-designer-row" data-row="' + index + '" data-original-name="' + esc(field.originalName || field.name || '') + '"><input class="editor-input" data-key="name" value="' + esc(field.name || '') + '" placeholder="字段名" aria-label="字段名"><input class="editor-input" data-key="type" value="' + esc(field.type || 'VARCHAR2(255)') + '" placeholder="类型" aria-label="字段类型"><label class="db-pro-check"><input type="checkbox" data-key="nullable"' + (field.nullable !== false ? ' checked' : '') + '> NULL</label><label class="db-pro-check"><input type="checkbox" data-key="primary"' + (field.primary ? ' checked' : '') + (field.primaryLocked ? ' disabled title="已有表请在约束页修改主键"' : '') + '> PK</label><input class="editor-input" data-key="defaultValue" value="' + esc(field.defaultValue || '') + '" placeholder="默认值"><input class="editor-input" data-key="comment" value="' + esc(field.comment || '') + '" placeholder="注释"><button type="button" class="btn btn-xs btn-danger" data-remove-row="' + index + '" aria-label="删除字段">删除</button></div>'; }).join('') || '<div class="db-pro-designer-empty">暂无字段，点击“添加字段”。</div>';
  }
  function readFieldRows(list) {
    return Array.from(list.querySelectorAll('.db-pro-designer-row[data-row]')).map(function (row) { const result = { originalName: row.dataset.originalName || '' }; row.querySelectorAll('[data-key]').forEach(function (input) { result[input.dataset.key] = input.type === 'checkbox' ? input.checked : input.value.trim(); }); return result; }).filter(function (field) { return field.name; });
  }
  function columnForField(field) {
    return { name: field.name, data_type: field.type || 'VARCHAR2(255)', nullable: field.nullable !== false, default: field.defaultValue || '', comment: field.comment || '', primary_key: !!field.primary };
  }
  function sameField(a, b) {
    return a && b && String(a.name || '').toUpperCase() === String(b.name || '').toUpperCase() && String(a.type || '').toUpperCase().replace(/\s+/g, ' ') === String(b.type || '').toUpperCase().replace(/\s+/g, ' ') && !!a.nullable === !!b.nullable && String(a.defaultValue || '') === String(b.defaultValue || '') && String(a.comment || '') === String(b.comment || '') && !!a.primary === !!b.primary;
  }
  function tableChanges(draft) {
    const original = draft.originalFields || [], current = draft.fields || [], changes = [];
    current.forEach(function (field) {
      const oldName = field.originalName || field.name, old = original.find(function (item) { return String(item.name).toUpperCase() === String(oldName).toUpperCase(); });
      if (!old) changes.push({ action: 'add_column', column: columnForField(field) });
      else if (String(old.name).toUpperCase() !== String(field.name).toUpperCase()) changes.push({ action: 'rename_column', old_name: old.name, column: columnForField(field) });
      else if (!sameField(old, field)) changes.push({ action: 'alter_column', column: columnForField(field) });
    });
    original.forEach(function (field) { if (!current.some(function (item) { return String(item.originalName || item.name).toUpperCase() === String(field.name).toUpperCase(); })) changes.push({ action: 'drop_column', old_name: field.name, column: { name: field.name, data_type: field.type || 'VARCHAR2(255)' } }); });
    return changes;
  }
  function constraintFromDraft(item, table) {
    const type = String(item.type || 'primary').toLowerCase();
    const columns = String(item.columns || '').split(',').map(function (value) { return value.trim(); }).filter(Boolean);
    const result = { name: item.name, table: item.table || table, type: type, columns: columns, expression: item.expression || '', referenced_schema: item.referencedSchema || '', referenced_table: item.referencedTable || '', referenced_columns: String(item.referencedColumns || '').split(',').map(function (value) { return value.trim(); }).filter(Boolean), on_delete: item.onDelete || '' };
    return result;
  }
  function detailsFromDraft(draft) {
    const context = draft.context, type = String(context.type || 'TABLE').toLowerCase(), action = draft.existing ? 'alter' : 'create', details = [];
    if (type === 'table') {
      const detail = { action: action, object_type: 'table', schema: context.schema || '', name: context.object, columns: draft.fields.map(columnForField), primary_key: draft.fields.filter(function (field) { return field.primary; }).map(function (field) { return field.name; }), changes: draft.existing ? tableChanges(draft) : [] };
      if (action === 'create' || detail.changes.length) details.push(detail);
      const initialIndexes = draft.initialIndexes || [], initialConstraints = draft.initialConstraints || [];
      draft.indexes.forEach(function (item) {
        if (!item.name || !item.columns) return;
        const old = initialIndexes.find(function (candidate) { return String(candidate.name || '').toUpperCase() === String(item.name || '').toUpperCase(); });
        if (old && sameIndex(old, item)) return;
        const index = { name: item.name, table: item.table || context.object, columns: String(item.columns).split(',').map(function (value) { return value.trim(); }).filter(Boolean), unique: !!item.unique, method: item.method || '' };
        details.push({ action: old ? 'alter' : 'create', object_type: 'index', schema: context.schema || '', name: item.name, table: index.table, index: index });
      });
      initialIndexes.forEach(function (old) {
        if (draft.indexes.some(function (item) { return String(item.name || '').toUpperCase() === String(old.name || '').toUpperCase(); })) return;
        const index = { name: old.name, table: old.table || context.object, columns: String(old.columns || '').split(',').map(function (value) { return value.trim(); }).filter(Boolean), unique: !!old.unique, method: old.method || '' };
        // Preview validates destructive operations too; carry the explicit
        // confirmation marker so the preview can show the generated DROP.
        details.push({ action: 'drop', object_type: 'index', schema: context.schema || '', name: old.name, table: index.table, index: index, confirm: true });
      });
      draft.constraints.forEach(function (item) {
        if (!item.name || (!item.columns && !item.expression)) return;
        const old = initialConstraints.find(function (candidate) { return String(candidate.name || '').toUpperCase() === String(item.name || '').toUpperCase(); });
        if (old && sameConstraint(old, item)) return;
        const constraint = constraintFromDraft(item, context.object);
        details.push({ action: old ? 'alter' : 'create', object_type: 'constraint', schema: context.schema || '', name: item.name, table: constraint.table, constraint: constraint });
      });
      initialConstraints.forEach(function (old) {
        if (draft.constraints.some(function (item) { return String(item.name || '').toUpperCase() === String(old.name || '').toUpperCase(); })) return;
        const constraint = constraintFromDraft(old, context.object);
        details.push({ action: 'drop', object_type: 'constraint', schema: context.schema || '', name: old.name, table: constraint.table, constraint: constraint, confirm: true });
      });
    } else if (type === 'view') details.push({ action: action, object_type: 'view', schema: context.schema || '', name: context.object, definition: draft.definition || '' });
    else if (type === 'index') details.push({ action: action, object_type: 'index', schema: context.schema || '', name: draft.index.name, table: draft.index.table, index: { name: draft.index.name, table: draft.index.table, columns: String(draft.index.columns || '').split(',').map(function (value) { return value.trim(); }).filter(Boolean), unique: !!draft.index.unique, method: draft.index.method || '' } });
    else if (type === 'constraint') details.push({ action: action, object_type: 'constraint', schema: context.schema || '', name: draft.constraint.name, table: draft.constraint.table, constraint: constraintFromDraft(draft.constraint, draft.constraint.table) });
    else if (type === 'sequence') details.push({ action: action, object_type: 'sequence', schema: context.schema || '', name: draft.sequence.name, sequence: { name: draft.sequence.name, start_with: Number(draft.sequence.startWith || 0), increment: Number(draft.sequence.increment || 0), min_value: Number(draft.sequence.minValue || 0), max_value: Number(draft.sequence.maxValue || 0), cache: Number(draft.sequence.cache || 0), cycle: !!draft.sequence.cycle, order: !!draft.sequence.order } });
    return details;
  }
  function localStatement(detail) {
    const name = (detail.schema ? quoteIdentifier(detail.schema) + '.' : '') + quoteIdentifier(detail.name), type = detail.object_type;
    if (type === 'table') {
      if (detail.action === 'create') return 'CREATE TABLE ' + name + ' (\n  ' + detail.columns.map(function (col) { return quoteIdentifier(col.name) + ' ' + col.data_type + (col.nullable === false ? ' NOT NULL' : '') + (col.default ? ' DEFAULT ' + col.default : '') + (col.primary_key ? ' PRIMARY KEY' : ''); }).join(',\n  ') + '\n)';
      return (detail.changes || []).map(function (change) { if (change.action === 'add_column') return 'ALTER TABLE ' + name + ' ADD ' + quoteIdentifier(change.column.name) + ' ' + change.column.data_type; if (change.action === 'drop_column') return 'ALTER TABLE ' + name + ' DROP COLUMN ' + quoteIdentifier(change.old_name); if (change.action === 'rename_column') return 'ALTER TABLE ' + name + ' RENAME COLUMN ' + quoteIdentifier(change.old_name) + ' TO ' + quoteIdentifier(change.column.name); return 'ALTER TABLE ' + name + ' MODIFY ' + quoteIdentifier(change.column.name) + ' ' + change.column.data_type; }).join(';\n');
    }
    if (type === 'view') return (detail.action === 'drop' ? 'DROP VIEW ' + name : 'CREATE OR REPLACE VIEW ' + name + ' AS ' + String(detail.definition || '').trim());
    if (type === 'index') return (detail.action === 'drop' ? 'DROP INDEX ' + name : 'CREATE ' + (detail.index && detail.index.unique ? 'UNIQUE ' : '') + 'INDEX ' + name + ' ON ' + quoteIdentifier(detail.index && detail.index.table) + ' (' + ((detail.index && detail.index.columns) || []).map(quoteIdentifier).join(', ') + ')');
    if (type === 'constraint') return 'ALTER TABLE ' + quoteIdentifier(detail.constraint && detail.constraint.table) + ' ADD CONSTRAINT ' + name + ' ' + (detail.constraint && detail.constraint.type || 'PRIMARY') + ' (' + ((detail.constraint && detail.constraint.columns) || []).map(quoteIdentifier).join(', ') + ')';
    if (type === 'sequence') return 'CREATE SEQUENCE ' + name + (detail.sequence && detail.sequence.start_with ? ' START WITH ' + detail.sequence.start_with : '') + (detail.sequence && detail.sequence.increment ? ' INCREMENT BY ' + detail.sequence.increment : '');
    return '';
  }
  function buildDDL(context, fields, existing, indexes, constraints) {
    const draft = { context: context, fields: fields || [], originalFields: existing ? [] : [], indexes: indexes || [], constraints: constraints || [], existing: !!existing };
    return detailsFromDraft(draft).map(localStatement).filter(Boolean).join('\n\n');
  }
  function designerDraft(context, viewer) {
    const fields = String(context.type).toUpperCase() === 'TABLE' && viewer && viewer.querySelector ? parseFields(viewer) : [];
    fields.forEach(function (field) { field.originalName = field.name; });
    if (!context.isNew) fields.forEach(function (field) { field.primaryLocked = true; });
    return { context: context, fields: cloneFields(fields), originalFields: cloneFields(fields), indexes: [], constraints: [], initialIndexes: [], initialConstraints: [], definition: '', initialDefinition: '', index: { name: context.object, table: '', columns: '', unique: false, method: '' }, constraint: { name: context.object + '_PK', table: context.object, type: 'primary', columns: '', expression: '', referencedSchema: '', referencedTable: '', referencedColumns: '', onDelete: '' }, sequence: { name: context.object, startWith: 1, increment: 1, minValue: 0, maxValue: 0, cache: 20, cycle: false, order: false }, existing: !context.isNew, hydrated: !!context.isNew, changed: false, initialIndex: null, initialConstraint: null, initialSequence: null };
  }
  function openDesigner(context, viewer) {
    const draft = designerDraft(context, viewer), type = String(context.type || 'TABLE').toUpperCase(), isTable = type === 'TABLE';
    const body = document.createElement('div');
    const tabs = isTable ? '<button type="button" class="active" data-pane="fields">字段</button><button type="button" data-pane="indexes">索引</button><button type="button" data-pane="constraints">约束</button><button type="button" data-pane="ddl">DDL 预览</button>' : '<button type="button" class="active" data-pane="definition">' + typeLabel(type) + '</button><button type="button" data-pane="ddl">DDL 预览</button>';
     const tablePanes = isTable ? '<section data-pane-body="fields"><div class="db-pro-designer-note">' + (draft.existing ? '修改会生成最小 ALTER 变更；删除字段需要在提交时二次确认。已有表主键请在“约束”页修改。' : '创建表时字段、主键、索引和约束会拆成多条类型化请求。') + '</div><div class="db-pro-designer-list" id="db-pro-designer-fields"></div><button type="button" class="btn btn-xs" id="db-pro-designer-add-field">添加字段</button></section><section data-pane-body="indexes" hidden><div class="db-pro-designer-list" id="db-pro-designer-indexes"></div><button type="button" class="btn btn-xs" id="db-pro-designer-add-index">添加索引</button></section><section data-pane-body="constraints" hidden><div class="db-pro-designer-list" id="db-pro-designer-constraints"></div><button type="button" class="btn btn-xs" id="db-pro-designer-add-constraint">添加约束</button></section>' : '<section data-pane-body="definition"><div class="db-pro-designer-note">视图定义只接受后端校验通过的只读 SELECT/WITH。</div><textarea id="db-pro-designer-definition" class="db-pro-designer-definition" spellcheck="false" placeholder="SELECT …"></textarea></section>';
    const specialPane = !isTable ? '<section data-pane-body="definition" hidden><div class="db-pro-designer-note">请填写定义。</div></section>' : '';
    body.innerHTML = '<div class="db-pro-designer-tabs">' + tabs + '</div>' + tablePanes + specialPane + '<section data-pane-body="ddl" hidden><textarea id="db-pro-designer-ddl" class="editor-content" spellcheck="false" aria-label="DDL 预览"></textarea><div class="db-pro-designer-note" id="db-pro-designer-status">点击“刷新 DDL 预览”会调用 typed preview；提交时后端重新生成并校验 DDL。</div></section>' + (!isTable ? '<div id="db-pro-designer-special" class="db-pro-designer-special"></div>' : '');
    const modal = createModal({ title: '对象设计器 · ' + typeLabel(type) + ' ' + objectName(context), body: body, actions: [
      { text: '刷新 DDL 预览', className: 'btn', close: false, onClick: async function () { await refreshDDL(true); } },
      { text: '提交对象', className: 'btn btn-primary', close: false, onClick: async function (button) {
        const details = refreshDraft(), ddl = details.map(localStatement).filter(Boolean).join('\n\n');
        if (!details.length) { toast('没有检测到可提交的对象变更', 'warn'); return; }
        const drop = details.some(function (detail) { return detail.action === 'drop'; });
        if (!window.confirm('即将提交 ' + details.length + ' 条对象变更到 ' + objectName(context) + (drop ? '；包含删除操作，' : '') + '请确认已审阅预览。')) return;
        button.disabled = true; button.textContent = '提交中…';
        try {
          for (const detail of details) {
            const request = Object.assign({}, detail, { source_id: sourceId(), confirm: true });
            emit('kairo:database-ddl', { request: request, detail: detail });
            const adapter = F.getAdapter && F.getAdapter('ddl');
            if (adapter) await adapter(request);
            else await api('POST', '/api/database/object-studio/apply', request);
          }
          toast('对象变更已提交', 'ok'); emit('kairo:database-object-changed', { sourceId: sourceId(), object: context }); modal.close();
          const refresh = id('db-meta-refresh'); if (refresh) refresh.click();
        } catch (error) { toast(error && Number(error.status) === 404 ? '对象设计器接口尚未部署' : '对象提交失败：' + (error.message || error), error && Number(error.status) === 404 ? 'info' : 'err'); button.disabled = false; button.textContent = '提交对象'; }
      } },
      { text: '关闭', className: 'btn', close: true }
    ] });
    const fieldsHost = body.querySelector('#db-pro-designer-fields'), indexesHost = body.querySelector('#db-pro-designer-indexes'), constraintsHost = body.querySelector('#db-pro-designer-constraints'), ddl = body.querySelector('#db-pro-designer-ddl'), status = body.querySelector('#db-pro-designer-status');
    function renderIndexes() { if (!indexesHost) return; indexesHost.innerHTML = draft.indexes.map(function (index, i) { return '<div class="db-pro-designer-row" data-index="' + i + '"><input class="editor-input" data-key="name" value="' + esc(index.name || '') + '" placeholder="索引名"><input class="editor-input" data-key="table" value="' + esc(index.table || context.object) + '" placeholder="表名"><input class="editor-input" data-key="columns" value="' + esc(index.columns || '') + '" placeholder="列名（逗号分隔）"><label class="db-pro-check"><input type="checkbox" data-key="unique"' + (index.unique ? ' checked' : '') + '> UNIQUE</label><button type="button" class="btn btn-xs btn-danger" data-remove-index="' + i + '">删除</button></div>'; }).join('') || '<div class="db-pro-designer-empty">暂无索引。</div>'; }
    function renderConstraints() { if (!constraintsHost) return; constraintsHost.innerHTML = draft.constraints.map(function (constraint, i) { return '<div class="db-pro-designer-row" data-constraint="' + i + '"><input class="editor-input" data-key="name" value="' + esc(constraint.name || '') + '" placeholder="约束名"><input class="editor-input" data-key="table" value="' + esc(constraint.table || context.object) + '" placeholder="表名"><select class="editor-input" data-key="type"><option value="primary">PRIMARY KEY</option><option value="unique">UNIQUE</option><option value="foreign">FOREIGN KEY</option><option value="check">CHECK</option><option value="not_null">NOT NULL</option></select><input class="editor-input" data-key="columns" value="' + esc(constraint.columns || '') + '" placeholder="本地列（逗号分隔）"><input class="editor-input" data-key="expression" value="' + esc(constraint.expression || '') + '" placeholder="CHECK 表达式"><input class="editor-input" data-key="referencedTable" value="' + esc(constraint.referencedTable || '') + '" placeholder="外键引用表"><input class="editor-input" data-key="referencedColumns" value="' + esc(constraint.referencedColumns || '') + '" placeholder="外键引用列"><button type="button" class="btn btn-xs btn-danger" data-remove-constraint="' + i + '">删除</button></div>'; }).join('') || '<div class="db-pro-designer-empty">暂无约束。</div>'; constraintsHost.querySelectorAll('[data-constraint]').forEach(function (row, i) { const input = row.querySelector('[data-key="type"]'); if (input) input.value = draft.constraints[i].type || 'primary'; }); }
    function readList(host) { return host ? Array.from(host.querySelectorAll('.db-pro-designer-row')).map(function (row) { const item = {}; row.querySelectorAll('[data-key]').forEach(function (input) { item[input.dataset.key] = input.type === 'checkbox' ? input.checked : input.value.trim(); }); return item; }) : []; }
    function renderSpecial() {
      const host = body.querySelector('#db-pro-designer-special'); if (!host) return;
      if (type === 'INDEX') host.innerHTML = '<div class="db-pro-row-grid"><label>索引名<input class="editor-input" data-special="index-name" value="' + esc(draft.index.name) + '"></label><label>表名<input class="editor-input" data-special="index-table" value="' + esc(draft.index.table) + '"></label><label>列名（逗号分隔）<input class="editor-input" data-special="index-columns" value="' + esc(draft.index.columns) + '"></label><label class="db-pro-check"><input type="checkbox" data-special="index-unique"' + (draft.index.unique ? ' checked' : '') + '> UNIQUE</label></div>';
      else if (type === 'CONSTRAINT') host.innerHTML = '<div class="db-pro-row-grid"><label>约束名<input class="editor-input" data-special="constraint-name" value="' + esc(draft.constraint.name) + '"></label><label>表名<input class="editor-input" data-special="constraint-table" value="' + esc(draft.constraint.table) + '"></label><label>类型<select class="editor-input" data-special="constraint-type"><option value="primary">PRIMARY KEY</option><option value="unique">UNIQUE</option><option value="foreign">FOREIGN KEY</option><option value="check">CHECK</option><option value="not_null">NOT NULL</option></select></label><label>本地列<input class="editor-input" data-special="constraint-columns" value="' + esc(draft.constraint.columns) + '"></label><label>CHECK 表达式<input class="editor-input" data-special="constraint-expression" value="' + esc(draft.constraint.expression) + '"></label><label>引用 Schema<input class="editor-input" data-special="constraint-ref-schema" value="' + esc(draft.constraint.referencedSchema) + '"></label><label>引用表<input class="editor-input" data-special="constraint-ref-table" value="' + esc(draft.constraint.referencedTable) + '"></label><label>引用列<input class="editor-input" data-special="constraint-ref-columns" value="' + esc(draft.constraint.referencedColumns) + '"></label><label>ON DELETE<select class="editor-input" data-special="constraint-on-delete"><option value="">默认</option><option>CASCADE</option><option>SET NULL</option><option>RESTRICT</option><option>NO ACTION</option></select></label></div>';
      else if (type === 'SEQUENCE') host.innerHTML = '<div class="db-pro-row-grid"><label>序列名<input class="editor-input" data-special="sequence-name" value="' + esc(draft.sequence.name) + '"></label><label>起始值<input class="editor-input" type="number" data-special="sequence-start" value="' + esc(draft.sequence.startWith) + '"></label><label>增量<input class="editor-input" type="number" data-special="sequence-increment" value="' + esc(draft.sequence.increment) + '"></label><label>最小值<input class="editor-input" type="number" data-special="sequence-min" value="' + esc(draft.sequence.minValue) + '"></label><label>最大值（0=默认）<input class="editor-input" type="number" data-special="sequence-max" value="' + esc(draft.sequence.maxValue) + '"></label><label>缓存<input class="editor-input" type="number" data-special="sequence-cache" value="' + esc(draft.sequence.cache) + '"></label><label class="db-pro-check"><input type="checkbox" data-special="sequence-cycle"' + (draft.sequence.cycle ? ' checked' : '') + '> CYCLE</label><label class="db-pro-check"><input type="checkbox" data-special="sequence-order"' + (draft.sequence.order ? ' checked' : '') + '> ORDER</label></div>';
      const special = host.querySelector('[data-special="constraint-type"]'); if (special) special.value = draft.constraint.type;
    }
    function refreshDraft() {
      if (fieldsHost) draft.fields = readFieldRows(fieldsHost);
      draft.indexes = readList(indexesHost); draft.constraints = readList(constraintsHost);
      const definition = body.querySelector('#db-pro-designer-definition'); if (definition) draft.definition = definition.value;
      if (type === 'INDEX') { draft.index = { name: body.querySelector('[data-special="index-name"]').value.trim(), table: body.querySelector('[data-special="index-table"]').value.trim(), columns: body.querySelector('[data-special="index-columns"]').value.trim(), unique: body.querySelector('[data-special="index-unique"]').checked, method: '' }; }
      if (type === 'CONSTRAINT') draft.constraint = { name: body.querySelector('[data-special="constraint-name"]').value.trim(), table: body.querySelector('[data-special="constraint-table"]').value.trim(), type: body.querySelector('[data-special="constraint-type"]').value, columns: body.querySelector('[data-special="constraint-columns"]').value.trim(), expression: body.querySelector('[data-special="constraint-expression"]').value.trim(), referencedSchema: body.querySelector('[data-special="constraint-ref-schema"]').value.trim(), referencedTable: body.querySelector('[data-special="constraint-ref-table"]').value.trim(), referencedColumns: body.querySelector('[data-special="constraint-ref-columns"]').value.trim(), onDelete: body.querySelector('[data-special="constraint-on-delete"]').value };
      if (type === 'SEQUENCE') draft.sequence = { name: body.querySelector('[data-special="sequence-name"]').value.trim(), startWith: body.querySelector('[data-special="sequence-start"]').value, increment: body.querySelector('[data-special="sequence-increment"]').value, minValue: body.querySelector('[data-special="sequence-min"]').value, maxValue: body.querySelector('[data-special="sequence-max"]').value, cache: body.querySelector('[data-special="sequence-cache"]').value, cycle: body.querySelector('[data-special="sequence-cycle"]').checked, order: body.querySelector('[data-special="sequence-order"]').checked };
      // A freshly opened existing special object is intentionally inert until
      // its typed structure has arrived. This prevents a slow metadata call
      // from turning the default form into an accidental ALTER request.
      if (draft.existing && !draft.hydrated && type !== 'TABLE') return [];
      if (draft.existing && draft.hydrated) {
        const same = function (a, b) { return JSON.stringify(a || null) === JSON.stringify(b || null); };
        if (type === 'VIEW' && String(draft.definition || '') === String(draft.initialDefinition || '')) return [];
        if (type === 'INDEX' && same(draft.index, draft.initialIndex)) return [];
        if (type === 'CONSTRAINT' && same(draft.constraint, draft.initialConstraint)) return [];
        if (type === 'SEQUENCE' && same(draft.sequence, draft.initialSequence)) return [];
      }
      return detailsFromDraft(draft);
    }
    async function refreshDDL(remote) {
      const details = refreshDraft(), local = details.map(localStatement).filter(Boolean).join('\n\n');
      if (ddl) ddl.value = local;
      if (!remote || !details.length) return local;
      try {
        const plans = [];
        for (const detail of details) plans.push(await api('POST', '/api/database/object-studio/preview', Object.assign({ source_id: sourceId() }, detail)));
        const statements = plans.map(function (plan) { const value = plan && (plan.plan || plan); return value && value.statements || []; }).reduce(function (all, part) { return all.concat(part); }, []);
        if (ddl && statements.length) ddl.value = statements.join('\n\n');
        if (status) status.textContent = '后端 preview 已返回，提交时会再次校验。';
      } catch (error) { if (status) status.textContent = '预览失败：' + (error.message || error); toast('DDL 预览失败：' + (error.message || error), 'warn'); }
      return ddl ? ddl.value : local;
    }
    if (fieldsHost) renderFieldRows(fieldsHost, draft.fields); renderIndexes(); renderConstraints(); renderSpecial(); refreshDDL(false);
    async function hydrateExisting() {
      if (!draft.existing || draft.changed) return;
      try {
        const query = '?source_id=' + encodeURIComponent(sourceId()) + '&schema=' + encodeURIComponent(context.schema || '') + '&object=' + encodeURIComponent(context.object || '') + '&type=' + encodeURIComponent(type);
        const response = await api('GET', '/api/database/object-studio/structure' + query);
        if (draft.changed) return;
        const structure = response && (response.structure || response);
        if (!structure) return;
        const fields = Array.isArray(structure.fields) ? structure.fields.map(function (field) { return { name: field.name || '', type: field.data_type || field.database_type || field.type || 'VARCHAR2(255)', nullable: field.nullable !== false, primary: !!(field.primary_key || field.primaryKey), defaultValue: field.default || field.defaultValue || '', comment: field.comment || '' }; }).filter(function (field) { return field.name; }) : null;
         if (fields && type === 'TABLE') { fields.forEach(function (field) { field.originalName = field.name; field.primaryLocked = true; }); draft.fields = fields; draft.originalFields = cloneFields(fields); }
        if (type === 'TABLE') {
          if (Array.isArray(structure.indexes)) {
            draft.indexes = structure.indexes.map(function (item) { return { name: item.name || '', table: context.object, columns: (item.columns || []).join(', '), unique: String(item.uniqueness || '').toUpperCase() === 'UNIQUE', method: item.type || '' }; }).filter(function (item) { return item.name; });
            draft.initialIndexes = draft.indexes.map(function (item) { return Object.assign({}, item); });
          }
          if (Array.isArray(structure.constraints)) {
            draft.constraints = structure.constraints.map(function (item) { return { name: item.name || '', table: context.object, type: String(item.type || 'primary').toLowerCase(), columns: item.columns || '', expression: item.detail || '', referencedSchema: '', referencedTable: '', referencedColumns: '', onDelete: '' }; }).filter(function (item) { return item.name; });
            draft.initialConstraints = draft.constraints.map(function (item) { return Object.assign({}, item); });
          }
        } else if (type === 'VIEW') {
          draft.definition = structure.source_text || structure.sourceText || structure.definition || '';
          draft.initialDefinition = draft.definition;
        } else if (type === 'INDEX' && structure.index) {
          const item = structure.index; draft.index = { name: item.name || context.object, table: item.table || context.object, columns: (item.columns || []).join(', '), unique: String(item.uniqueness || '').toUpperCase() === 'UNIQUE', method: item.type || '' }; draft.initialIndex = Object.assign({}, draft.index);
        } else if (type === 'CONSTRAINT' && structure.constraint) {
          const item = structure.constraint; draft.constraint = { name: item.name || context.object, table: item.table || context.object, type: String(item.type || 'primary').toLowerCase(), columns: item.columns || '', expression: item.detail || '', referencedSchema: item.referencedSchema || '', referencedTable: item.referencedTable || '', referencedColumns: item.referencedColumns || '', onDelete: item.onDelete || '' }; draft.initialConstraint = Object.assign({}, draft.constraint);
        } else if (type === 'SEQUENCE' && structure.sequence) {
          const item = structure.sequence; draft.sequence = { name: item.name || context.object, startWith: String(item.start_with == null ? 1 : item.start_with), increment: String(item.increment == null ? 1 : item.increment), minValue: String(item.min_value == null ? 0 : item.min_value), maxValue: String(item.max_value == null ? 0 : item.max_value), cache: String(item.cache == null ? 20 : item.cache), cycle: !!item.cycle, order: !!item.order }; draft.initialSequence = Object.assign({}, draft.sequence);
        }
        if (type === 'VIEW' && !draft.definition && structure.ddl) draft.definition = structure.ddl;
        if (type === 'VIEW' && !draft.initialDefinition) draft.initialDefinition = draft.definition;
        draft.hydrated = true;
        if (fieldsHost) renderFieldRows(fieldsHost, draft.fields); renderIndexes(); renderConstraints(); renderSpecial(); refreshDDL(false);
      } catch (error) {
        if (status) status.textContent = '结构回填失败：' + (error.message || error) + '；可手工填写后预览。';
      }
    }
    hydrateExisting();
    body.querySelectorAll('[data-pane]').forEach(function (button) { button.onclick = function () { body.querySelectorAll('[data-pane]').forEach(function (x) { x.classList.toggle('active', x === button); }); body.querySelectorAll('[data-pane-body]').forEach(function (panel) { panel.hidden = panel.dataset.paneBody !== button.dataset.pane; }); }; });
     const addField = body.querySelector('#db-pro-designer-add-field'); if (addField) addField.onclick = function () { draft.fields.push({ name: '', type: sourceKind() === 'mysql' ? 'VARCHAR(255)' : 'VARCHAR2(255)', nullable: true, defaultValue: '', comment: '', primary: false, primaryLocked: !!draft.existing }); renderFieldRows(fieldsHost, draft.fields); refreshDDL(false); };
    const addIndex = body.querySelector('#db-pro-designer-add-index'); if (addIndex) addIndex.onclick = function () { draft.indexes.push({ name: '', table: context.object, columns: '', unique: false }); renderIndexes(); refreshDDL(false); };
    const addConstraint = body.querySelector('#db-pro-designer-add-constraint'); if (addConstraint) addConstraint.onclick = function () { draft.constraints.push({ name: '', table: context.object, type: 'primary', columns: '', expression: '' }); renderConstraints(); refreshDDL(false); };
    body.addEventListener('input', function () { draft.changed = true; refreshDDL(false); });
    body.addEventListener('click', function (event) { const removeField = event.target.closest('[data-remove-row]'), removeIndex = event.target.closest('[data-remove-index]'), removeConstraint = event.target.closest('[data-remove-constraint]'); if (removeField) { draft.fields.splice(Number(removeField.dataset.removeRow), 1); renderFieldRows(fieldsHost, draft.fields); refreshDDL(false); } if (removeIndex) { draft.indexes.splice(Number(removeIndex.dataset.removeIndex), 1); renderIndexes(); refreshDDL(false); } if (removeConstraint) { draft.constraints.splice(Number(removeConstraint.dataset.removeConstraint), 1); renderConstraints(); refreshDDL(false); } });
  }

  function normalizeCompileErrors(data) {
    const result = data && data.result ? data.result : data;
    const fn = result && result.function ? result.function : null;
    const list = Array.isArray(data) ? data : Array.isArray(result && (result.errors || result.compile_errors || result.error_list)) ? (result.errors || result.compile_errors || result.error_list) : Array.isArray(fn && fn.errors) ? fn.errors : result && result.error ? [{ message: result.error }] : data && data.error ? [{ message: data.error }] : [];
    return list.map(function (item) { item = item || {}; const message = typeof item === 'string' ? item : item.message || item.text || item.error || JSON.stringify(item); const parsed = F.parseErrorLocation ? F.parseErrorLocation(message) : null; const position = Number(item.position || 0); return { message: message, line: Number(item.line || (parsed && parsed.line) || 0), column: Number(item.column || (parsed && parsed.column) || 1), position: position, offset: position > 0 ? position - 1 : undefined, severity: item.severity || item.level || item.attribute || 'error' }; });
  }
  function functionSourceFromViewer(viewer) { const code = viewer.querySelector('.db-obj-ddl-code'); return code ? code.textContent : ''; }
  async function loadFunctionSource(context, initial) {
    const key = sourceId() + '|' + context.schema + '|' + context.object;
    if (state.sourceCache[key]) return state.sourceCache[key];
    const adapter = F.getAdapter && F.getAdapter('loadSource');
    if (adapter) { try { const result = await adapter({ sourceId: sourceId(), schema: context.schema, object: context.object, type: context.type }); const text = typeof result === 'string' ? result : result.source || result.source_text || result.ddl || result.function && result.function.source || ''; state.sourceCache[key] = text; return text; } catch (_) {} }
    try { const result = await api('GET', '/api/database/object-studio/function/source?source_id=' + encodeURIComponent(sourceId()) + '&schema=' + encodeURIComponent(context.schema) + '&name=' + encodeURIComponent(context.object)); const fn = result.function || result; const text = fn.source || fn.source_text || fn.ddl || ''; if (text) state.sourceCache[key] = text; return text || initial; }
    catch (_) { return initial; }
  }
  function openFunctionEditor(context, viewer) {
    const body = document.createElement('div');
    body.innerHTML = '<div class="db-pro-function-toolbar"><span class="db-pro-function-status" id="db-pro-function-status">源码只在点击“编译”后提交</span><button type="button" class="btn btn-xs" id="db-pro-function-format">格式化</button></div><textarea id="db-pro-function-source" class="db-pro-function-source" spellcheck="false" aria-label="Function 源码"></textarea><div id="db-pro-function-errors" class="db-pro-function-errors" role="status"></div>';
    const modal = createModal({ title: 'Function 编辑 · ' + objectName(context), body: body, actions: [
      { text: '编译', className: 'btn btn-primary', close: false, onClick: compile },
      { text: '复制源码', className: 'btn', close: false, onClick: function () { const copy = K.core && K.core.copyToClipboard; if (copy) Promise.resolve(copy(source.value)).then(function () { toast('Function 源码已复制', 'ok'); }); } },
      { text: '关闭', className: 'btn', close: true }
    ] });
    const source = body.querySelector('#db-pro-function-source'), errors = body.querySelector('#db-pro-function-errors'), status = body.querySelector('#db-pro-function-status');
    source.value = functionSourceFromViewer(viewer);
    loadFunctionSource(context, source.value).then(function (text) { if (text && !source.value.trim()) source.value = text; });
    body.querySelector('#db-pro-function-format').onclick = function () { if (F.formatSQL) source.value = F.formatSQL(source.value); source.dispatchEvent(new Event('input')); };
    function renderErrors(items) {
      errors.innerHTML = items.length ? '<div class="db-pro-function-error-summary">编译发现 ' + items.length + ' 个问题</div>' + items.map(function (item, index) { return '<button type="button" class="db-pro-function-error-row" data-error="' + index + '"><span>' + esc(item.severity) + '</span><strong>第 ' + (item.line || '?') + ' 行，第 ' + (item.column || '?') + ' 列</strong><em>' + esc(item.message) + '</em></button>'; }).join('') : '<div class="db-pro-function-success">编译成功，未发现错误。</div>';
      errors.querySelectorAll('[data-error]').forEach(function (button) { button.onclick = function () { const item = items[Number(button.dataset.error)], offset = F.locationOffset ? F.locationOffset(source.value, item) : 0; source.focus(); source.setSelectionRange(offset, Math.min(source.value.length, offset + 1)); const lineHeight = parseFloat(getComputedStyle(source).lineHeight) || 20; source.scrollTop = Math.max(0, (item.line - 3) * lineHeight); }; });
    }
    async function compile(button) {
      const detail = { source_id: sourceId(), schema: context.schema, name: context.object, source: source.value, replace: true, confirm: true, sourceId: sourceId(), object: context.object, type: 'FUNCTION', dialect: 'oracle' };
      emit('kairo:database-function-compile', detail); button.disabled = true; button.textContent = '编译中…'; status.textContent = '正在编译…'; errors.innerHTML = '';
      try {
        let result;
        const adapter = F.getAdapter && F.getAdapter('compile');
        if (adapter) result = await adapter(detail);
        else result = await api('POST', '/api/database/object-studio/function/compile', { source_id: detail.source_id, schema: detail.schema, name: detail.name, source: detail.source, replace: detail.replace, confirm: detail.confirm });
        const list = normalizeCompileErrors(result); renderErrors(list); status.textContent = list.length ? '编译失败' : '编译成功';
      } catch (error) {
        const list = normalizeCompileErrors(error.data || { error: error.message }); renderErrors(list.length ? list : [{ message: error.message, line: 0, column: 1, severity: 'error' }]); status.textContent = error.status === 404 ? '等待后端编译接口' : '编译失败';
      } finally { button.disabled = false; button.textContent = '编译'; }
    }
  }

  function decorateViewer(viewer) {
    if (!viewer || viewer.hidden) return;
    const context = currentObject(viewer); if (!context) return;
    state.viewer = viewer; state.current = context;
    const actions = viewer.querySelector('.db-obj-actions'); if (!actions) return;
    if ((context.type === 'TABLE' || context.type === 'VIEW' || context.type === 'INDEX' || context.type === 'CONSTRAINT' || context.type === 'SEQUENCE') && !actions.querySelector('[data-db-pro-designer]')) {
      const button = document.createElement('button'); button.type = 'button'; button.className = 'btn btn-xs'; button.dataset.dbProDesigner = '1'; button.textContent = '可视化设计'; button.title = '打开字段、索引、约束与 DDL 设计器'; button.onclick = function () { openDesigner(context, viewer); }; actions.appendChild(button);
    }
    if (context.type === 'FUNCTION' && !actions.querySelector('[data-db-pro-function]')) {
      const button = document.createElement('button'); button.type = 'button'; button.className = 'btn btn-xs btn-primary'; button.dataset.dbProFunction = '1'; button.textContent = '编辑源码'; button.title = '编辑并编译 Function 源码'; button.onclick = function () { openFunctionEditor(context, viewer); }; actions.appendChild(button);
    }
  }
  function addCreateObjectButton(root) {
    const title = root.querySelector('.db-meta .db-pane-title .db-pane-actions');
    if (!title || title.querySelector('[data-db-pro-create]')) return;
    const button = document.createElement('button'); button.type = 'button'; button.className = 'btn btn-xs'; button.dataset.dbProCreate = '1'; button.textContent = '新建对象'; button.title = '选择对象类型并打开可视化设计器'; button.onclick = function () {
      const host = document.createElement('div'); host.innerHTML = '<div class="db-pro-row-grid"><label>对象类型<select id="db-pro-create-type" class="editor-input"><option value="TABLE">表</option><option value="VIEW">视图</option><option value="INDEX">索引</option><option value="CONSTRAINT">约束</option><option value="SEQUENCE">序列</option></select></label><label>对象名<input id="db-pro-create-name" class="editor-input" value="NEW_TABLE" placeholder="对象名" autofocus></label></div><p class="db-pro-modal-note">提交前会先调用后端 preview；MySQL 不支持原生序列，服务端会拒绝该类型。</p>';
      const chooser = createModal({ title: '新建数据库对象', body: host, actions: [{ text: '继续', className: 'btn btn-primary', close: false, onClick: function () { const name = host.querySelector('#db-pro-create-name').value.trim(), type = host.querySelector('#db-pro-create-type').value; if (!name) { toast('对象名不能为空', 'warn'); return; } chooser.close(); const context = { schema: schemaName(), object: name, type: type, isNew: true }, fake = document.createElement('section'); fake.innerHTML = '<div class="db-obj-header"><div class="db-obj-title"><span class="db-obj-kind-tag">' + esc(type) + '</span><h3>' + esc(name) + '</h3></div></div>'; openDesigner(context, fake); } }, { text: '关闭', className: 'btn', close: true }] });
    }; title.appendChild(button);
  }
  function install(root) {
    if (!root || !root.querySelector) return;
    addCreateObjectButton(root); decorateViewer(root.querySelector('#db-object-viewer'));
  }
  function start() {
    if (state.started) return; state.started = true;
    const view = document.getElementById('view') || document.body;
    let updating = false;
    const safeInstall = function (target) {
      if (updating) return;
      updating = true;
      try {
        if (state.observer) state.observer.disconnect();
        install(target || document.getElementById('db-workspace') || view);
      } finally {
        if (state.observer) {
          state.observer.takeRecords();
          if (String(location.hash || '').indexOf('#/database') === 0) {
            state.observer.observe(view, { subtree: true, childList: true, attributes: true, attributeFilter: ['hidden', 'class'] });
          }
        }
        updating = false;
      }
    };
    state.observer = new MutationObserver(function () {
      if (String(location.hash || '').indexOf('#/database') !== 0) return;
      if (updating) return;
      if (state.scheduled) return;
      state.scheduled = true;
      requestAnimationFrame(function () {
        state.scheduled = false;
        if (String(location.hash || '').indexOf('#/database') !== 0) return;
        safeInstall();
      });
    });
    const syncRoute = function () {
      if (state.observer) state.observer.disconnect();
      if (String(location.hash || '').indexOf('#/database') !== 0) { state.viewer = null; state.current = null; return; }
      safeInstall();
    };
    window.addEventListener('hashchange', syncRoute);
    syncRoute();
  }

  D.openDesigner = openDesigner;
  D.openFunctionEditor = openFunctionEditor;
  D.buildDDL = buildDDL;
  D.normalizeCompileErrors = normalizeCompileErrors;
  D.start = start;
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start); else start();
})();
