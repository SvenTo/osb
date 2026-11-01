// Features of the builder. Each feature contributes YAML steps (and optionally
// artifact settings) to the generated configuration.
//
//   id, title, phrase (used in the summary sentence), desc, group
//   flavors: supported flavors (default: all)
//   requires: other feature ids that are switched on with it
//   params: [{id, label, value, type: 'text'|'textarea'|'check', options}]
//   steps(p, ctx): YAML text of the steps, indented by 2 spaces
//   artifacts(p, ctx): {target: {key: yamlValue}}

/** YAML scalar (double quoted when needed). */
export function q(s) {
  s = String(s);
  if (s === '' || /^[\s\-?:,[\]{}#&*!|>'"%@`]|[:#]\s|\s$|^(true|false|yes|no|null|~|[\d.+-]+)$/i.test(s) || s.includes('${{')) {
    return JSON.stringify(s);
  }
  return s;
}

const list = (s) => String(s).split(/[\s,]+/).map((x) => x.trim()).filter(Boolean);
const flow = (items) => '[' + items.map(q).join(', ') + ']';
const U = '"${{ args.user }}"';

export const FEATURES = [
  {
    id: 'sudo', group: 'Users', title: 'Administrator rights', phrase: 'sudo rights',
    desc: 'The user may use sudo (sudo group on Debian/Ubuntu, wheel on Arch).', default: true,
    steps: () => '',
  },
  {
    id: 'ssh', group: 'Access', title: 'SSH access', phrase: 'SSH access',
    desc: 'OpenSSH server, enabled, with your public key.',
    params: [{ id: 'key', label: 'Public key (optional)', type: 'textarea', value: '' }],
    steps: (p) => `
  - title: SSH server
    action: install-packages
    args: { packages: [openssh-server] }
  - action: enable-systemd-unit
    args:
      units:
        by-flavor: { arch: [sshd.service], default: [ssh.service] }
` + (p.key.trim() ? `  - action: authorize-ssh-key
    args:
      user: ${U}
      key: ${flow(p.key.split('\n').map((l) => l.trim()).filter(Boolean))}
` : ''),
  },
  {
    id: 'docker', group: 'Development', title: 'Docker', phrase: 'Docker',
    desc: 'Docker engine, enabled; the user is in the docker group.',
    steps: () => `
  - title: Docker
    action: install-packages
    args:
      packages:
        by-flavor: { arch: [docker, docker-buildx], default: [docker.io] }
  - action: enable-systemd-unit
    args: { units: [docker.service] }
  - action: add-user-to-group
    args: { user: ${U}, groups: [docker] }
`,
  },
  {
    id: 'devtools', group: 'Development', title: 'Developer tools', phrase: 'developer tools',
    desc: 'git, a compiler toolchain, curl, jq and an editor – plus languages.',
    params: [
      { id: 'go', label: 'Go', type: 'check', value: true },
      { id: 'node', label: 'Node.js', type: 'check', value: false },
      { id: 'python', label: 'Python', type: 'check', value: true },
    ],
    steps: (p) => {
      const arch = ['git', 'base-devel', 'curl', 'jq', 'vim'];
      const deb = ['git', 'build-essential', 'curl', 'jq', 'vim'];
      if (p.go) { arch.push('go'); deb.push('golang'); }
      if (p.node) { arch.push('nodejs', 'npm'); deb.push('nodejs', 'npm'); }
      if (p.python) { arch.push('python', 'python-pip'); deb.push('python3', 'python3-pip'); }
      return `
  - title: Developer tools
    action: install-packages
    args:
      packages:
        by-flavor:
          arch: ${flow(arch)}
          default: ${flow(deb)}
`;
    },
  },
  {
    id: 'kde', group: 'Desktop', title: 'KDE Plasma desktop', phrase: 'a KDE Plasma desktop',
    desc: 'Plasma with a login manager; boots into the graphical target.',
    params: [{ id: 'autologin', label: 'Log the user in automatically', type: 'check', value: true }],
    steps: (p) => `
  - title: KDE Plasma
    action: install-packages
    args:
      packages:
        by-flavor:
          arch: [plasma-meta, plasma-login-manager, konsole, dolphin, kate]
          default: [kde-plasma-desktop, sddm]
` + (p.autologin ? `  - title: Automatic login (Plasma login manager)
    flavors: [arch]
    action: plasma-autologin
    args: { user: ${U} }
  - title: Automatic login (SDDM)
    flavors: [debian]
    action: write-file
    args:
      path: /etc/sddm.conf.d/autologin.conf
      content: |
        [Autologin]
        User=\${{ args.user }}
        Session=plasma
` : '') + `  - title: Boot into the desktop
    action: run-command
    args: { command: systemctl set-default graphical.target }
`,
  },
  {
    id: 'rdp', group: 'Desktop', title: 'Remote desktop (RDP)', phrase: 'remote desktop over RDP',
    desc: 'Shares the Plasma session with KRdp (password in /root/rdp-password).', flavors: ['archlinux'], requires: ['kde'],
    params: [{ id: 'port', label: 'Port', value: '3389' }],
    steps: (p) => `
  - action: install-packages
    args: { packages: [krdp] }
  - title: RDP server
    action: krdp-server
    args: { user: ${U}, port: ${parseInt(p.port, 10) || 3389} }
`,
  },
  {
    id: 'nosleep', group: 'Desktop', title: 'Never suspend', phrase: 'no suspend',
    desc: 'Masks the sleep targets – for VMs that must stay reachable.',
    steps: () => `
  - action: disable-sleep
`,
  },
  {
    id: 'web', group: 'Services', title: 'Web server', phrase: 'an nginx web server',
    desc: 'nginx with a start page; port 80 exposed in images.',
    params: [{ id: 'title', label: 'Page title', value: 'Hello from osb' }],
    steps: (p) => `
  - title: Web server
    action: install-packages
    args: { packages: [nginx] }
  - title: Start page
    action: write-file
    args:
      path:
        by-flavor: { arch: /usr/share/nginx/html/index.html, default: /var/www/html/index.html }
      content: |
        <!doctype html>
        <title>${p.title.replace(/[<>]/g, '')}</title>
        <h1>${p.title.replace(/[<>]/g, '')}</h1>
  - action: enable-systemd-unit
    args: { units: [nginx.service] }
`,
    artifacts: () => ({ dockerfile: { expose: '[80]', cmd: '[nginx, -g, "daemon off;"]' } }),
  },
  {
    id: 'tls', group: 'Services', title: "Let's Encrypt certificate", phrase: 'a TLS certificate',
    desc: 'certbot certificate with automatic renewal (on the running machine only).',
    params: [{ id: 'domain', label: 'Domain', value: 'www.example.com' }, { id: 'email', label: 'E-mail', value: '' }],
    steps: (p) => `
  - title: TLS certificate
    targets: [shell, apply]
    action: letsencrypt-certificate
    args:
      domain: ${q(p.domain || 'www.example.com')}${p.email ? `
      email: ${q(p.email)}` : ''}
`,
  },
  {
    id: 'locale', group: 'System', title: 'Time zone & locale', phrase: 'my time zone and locale',
    params: [{ id: 'tz', label: 'Time zone', value: 'Europe/Berlin' }, { id: 'locale', label: 'Locale', value: 'en_US.UTF-8' }],
    desc: 'Sets the system time zone and default locale.',
    steps: (p) => `
  - action: set-timezone
    args: { timezone: ${q(p.tz || 'UTC')} }
  - action: set-locale
    args: { locale: ${q(p.locale || 'en_US.UTF-8')} }
`,
  },
  {
    id: 'hostname', group: 'System', title: 'Host name', phrase: 'a host name',
    desc: 'Set at run time (not baked into container images).',
    params: [{ id: 'name', label: 'Host name', value: 'devbox' }],
    steps: (p) => `
  - action: set-hostname
    args: { hostname: ${q(p.name || 'devbox')} }
`,
  },
  {
    id: 'swap', group: 'System', title: 'Swap file', phrase: 'a swap file',
    desc: 'Creates and enables a swap file (skipped in containers).',
    params: [{ id: 'size', label: 'Size', value: '2G' }],
    steps: (p) => `
  - action: add-swap-file
    args: { size: ${q(p.size || '2G')} }
`,
  },
  {
    id: 'packages', group: 'Software', title: 'More packages', phrase: 'extra packages',
    desc: 'Any packages from the distribution repositories.',
    params: [{ id: 'names', label: 'Packages', value: 'htop tmux' }],
    steps: (p) => list(p.names).length ? `
  - title: Extra packages
    action: install-packages
    args: { packages: ${flow(list(p.names))} }
` : '',
  },
  {
    id: 'aur', group: 'Software', title: 'AUR packages (pinned)', phrase: 'pinned AUR packages', flavors: ['archlinux'],
    desc: 'Built as an unprivileged user from a reviewed commit of the AUR repository. One "name commit" per line.',
    params: [{ id: 'pkgs', label: 'name commit', type: 'textarea', value: 'yay-bin a2812a593d4017a657bbedf98761db78072b5e4c' }],
    steps: (p) => p.pkgs.split('\n').map((l) => l.trim().split(/\s+/)).filter((x) => x[0]).map(([name, commit]) => `
  - title: AUR package ${name}
    action: install-aur-package
    args: { name: ${q(name)}, commit: ${q(commit || 'COMMIT')} }
`).join(''),
  },
  {
    id: 'script', group: 'Software', title: 'Custom commands', phrase: 'my own commands',
    desc: 'A shell script that runs at the end.',
    params: [{ id: 'script', label: 'Script', type: 'textarea', value: 'echo "Welcome to $(hostname)" > /etc/motd' }],
    steps: (p) => p.script.trim() ? `
  - title: Custom commands
    action: run-script
    args:
      script: |
${p.script.replace(/\s+$/, '').split('\n').map((l) => '        ' + l).join('\n')}
` : '',
  },
];
