import { api } from '../api.js';
import { h, clear, fmtBytes, fmtBinary, toast } from '../util.js';

// Setup: shown when Arcana starts without a library. Lists drives with
// their capacity and file system so the user can choose where to create one.
export async function setupView(main) {
  const r = await api.get('/api/setup/volumes');
  const path = h('input', { type: 'text', value: r.suggestedPath || '', style: 'width:min(520px,100%)', 'aria-label': 'Library folder' });
  const label = h('input', { type: 'text', value: 'Arcana Library', style: 'width:220px', 'aria-label': 'Library name' });

  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Create a library')),
    h('div', { class: 'body' },
      h('p', null, 'Arcana keeps the whole library — programs, reference content, settings and your own documents — in one folder, normally on an external drive. Choose where to create it. You can move the drive to another computer later; nothing depends on this computer.'),
      h('div', { class: 'row' }, path, label, h('button', { class: 'primary', onclick: create }, 'Create library')),
      h('p', { class: 'small muted', style: 'margin-top:6px' }, 'Recommended file system: exFAT (works on Windows, macOS and Linux and holds files larger than 4 GB).'))));

  const rows = r.volumes.map((v) => {
    const warn = v.fs.warnings || [];
    return h('tr', { class: 'clickable', onclick: () => { path.value = v.existingLibrary || joinPath(v.mountPoint, 'Arcana'); } },
      h('td', null, h('b', { class: 'mono' }, v.mountPoint), v.label ? h('div', { class: 'small muted' }, v.label) : null),
      h('td', null, v.fs.name, v.fs.maxFileSize ? h('div', { class: 'small', style: 'color:var(--warn)' }, `max file ${fmtBinary(v.fs.maxFileSize)}`) : null),
      h('td', { class: 'right num' }, fmtBytes(v.totalBytes), v.nominal ? h('div', { class: 'small muted' }, `sold as ${fmtBytes(v.nominal)}`) : null),
      h('td', { class: 'right num' }, fmtBytes(v.availBytes)),
      h('td', null, v.removable === true ? 'Removable' : v.removable === false ? (v.system ? 'System disk' : 'Internal') : 'Unknown',
        v.readOnly ? h('div', { class: 'small', style: 'color:var(--err)' }, 'read-only') : null),
      h('td', { class: 'small' }, v.existingLibrary ? h('b', null, 'Existing library: ', v.existingLibrary) : null,
        warn.map((w) => h('div', { class: 'muted' }, w))));
  });
  main.appendChild(h('section', { class: 'panel' },
    h('header', null, h('h2', null, 'Detected drives'), h('span', { class: 'small muted' }, 'Select a drive to use it')),
    h('div', { class: 'body flush table-wrap' }, h('table', null,
      h('thead', null, h('tr', null, h('th', null, 'Drive'), h('th', null, 'File system'), h('th', { class: 'right' }, 'Capacity'),
        h('th', { class: 'right' }, 'Free'), h('th', null, 'Type'), h('th', null, 'Notes'))),
      h('tbody', null, rows)))));

  async function create() {
    try {
      await api.post('/api/setup/init', { path: path.value.trim(), label: label.value.trim() });
      toast('Library ready.');
      location.hash = '#/library';
      location.reload();
    } catch (e) { toast(e.message); }
  }
  return null;
}

function joinPath(mount, name) {
  const sep = mount.includes('\\') ? '\\' : '/';
  return mount.endsWith(sep) ? mount + name : mount + sep + name;
}
