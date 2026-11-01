import { render, OutputView, debounce, encodeShare, gotoLine, tabIndent } from './osb.js';

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

const yaml = document.getElementById('demo-yaml');
const view = new OutputView(document.getElementById('demo-out-panel'), { title: 'Generated' });
view.onErrorLine = (line) => gotoLine(yaml, line);
let flavor = 'ubuntu';
let target = 'shell';

yaml.value = EXAMPLE;
tabIndent(yaml);

const update = async () => {
  view.show(await render({ config: yaml.value, flavor, target }).catch((e) => ({ error: String(e) })));
};
const later = debounce(update, 250);
yaml.addEventListener('input', later);

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
