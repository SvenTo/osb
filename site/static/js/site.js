// Theme toggle (light/dark). The choice is remembered per browser.
(function () {
  var btn = document.querySelector('.theme-toggle');
  if (!btn) return;
  btn.addEventListener('click', function () {
    var root = document.documentElement;
    var dark = root.dataset.theme
      ? root.dataset.theme === 'dark'
      : window.matchMedia('(prefers-color-scheme: dark)').matches;
    root.dataset.theme = dark ? 'light' : 'dark';
    try { localStorage.setItem('osb-theme', root.dataset.theme); } catch (e) { /* storage unavailable */ }
  });
})();

// Syntax highlighting for code blocks in the documentation.
(function () {
  if (!window.osbLang) return;
  var blocks = document.querySelectorAll('pre > code[class*="language-"]');
  for (var i = 0; i < blocks.length; i++) {
    var m = blocks[i].className.match(/language-(\S+)/);
    blocks[i].innerHTML = window.osbLang.highlight(blocks[i].textContent, m[1]);
  }
  // Unlabeled blocks that look like YAML
  var plain = document.querySelectorAll('.prose pre > code:not([class])');
  for (var j = 0; j < plain.length; j++) {
    if (/^\s*[\w-]+:(\s|$)/m.test(plain[j].textContent)) plain[j].innerHTML = window.osbLang.highlight(plain[j].textContent, 'yaml');
  }
})();
