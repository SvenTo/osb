import { render, OutputView, debounce, encodeShare, osbLang } from './osb.js';
import { Editor } from './editor.js';

const EXAMPLE = `name: webserver
description: A small web server

arguments:
  user: { default: admin }
  greeting: { default: Hello from osb }

steps:
  - title: Web server and SSH
    action: install-packages
    args:
      packages: [nginx, openssh-server, curl]

  - action: add-user
    args: { name: "\${{ args.user }}", shell: /bin/bash, sudo: true }

  - title: Start page
    action: write-file
    args:
      path:
        by-flavor:
          arch: /usr/share/nginx/html/index.html
          default: /var/www/html/index.html
      content: |
        <h1>\${{ args.greeting }}</h1>

  - action: enable-systemd-unit
    args:
      units:
        by-flavor:
          arch: [nginx.service, sshd.service]
          default: [nginx.service, ssh.service]
`;

const view = new OutputView(document.getElementById('demo-out-panel'), { title: 'Generated' });
let flavor = 'ubuntu';
let target = 'shell';

const update = async () => {
  const resp = await render({ config: yaml.value, flavor, target }).catch((e) => ({ error: String(e) }));
  yaml.markError(resp.error && +(/osb\.yaml:(\d+)/.exec(resp.error) || [])[1]);
  view.show(resp);
};
const later = debounce(update, 250);
const yaml = new Editor(document.getElementById('demo-yaml'), { value: EXAMPLE, label: 'Configuration', onChange: later });
view.onErrorLine = (line) => yaml.gotoLine(line);
osbLang().then((l) => yaml.setLang(l));

function segmented(id, attr, set) {
  const group = document.getElementById(id);
  group.addEventListener('click', (e) => {
    const b = e.target.closest('button');
    if (!b) return;
    for (const x of group.querySelectorAll('button')) x.setAttribute('aria-pressed', String(x === b));
    set(b.dataset[attr]);
    update();
  });
}
segmented('demo-flavor', 'flavor', (v) => { flavor = v; });
segmented('demo-target', 'target', (v) => { target = v; });

document.getElementById('demo-open').addEventListener('click', async (e) => {
  e.preventDefault();
  location.href = 'playground.html#c=' + await encodeShare(yaml.value) + '&f=' + flavor + '&t=' + target;
});

update();
