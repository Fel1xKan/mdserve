/* 侧边栏收起、移动端目录、复制链接。没有框架，没有依赖。 */
(function () {
  var KEY = 'mdserve_nav_collapsed';

  function collapsed() { return document.body.classList.contains('nav-collapsed'); }

  function setNav(on) {
    document.body.classList.toggle('nav-collapsed', on);
    try { localStorage.setItem(KEY, on ? '1' : '0'); } catch (e) { /* 隐私模式 */ }
    if (!on) document.body.classList.remove('nav-open');
  }

  try { if (localStorage.getItem(KEY) === '1') document.body.classList.add('nav-collapsed'); } catch (e) { /* ignore */ }

  var btn = document.getElementById('collapseBtn');
  if (btn) btn.addEventListener('click', function () { setNav(!collapsed()); });

  var nav = document.getElementById('navToggle');
  if (nav) nav.addEventListener('click', function () {
    // 移动端是抽屉，桌面端是收起状态，两种都要能一键打开。
    document.body.classList.remove('nav-collapsed');
    document.body.classList.toggle('nav-open');
  });

  // 目录树：点箭头折叠，点目录名进入该目录（有首页时）。
  document.querySelectorAll('.node-dir > .node-label').forEach(function (label) {
    var dir = label.parentElement;
    var hasLink = !!label.querySelector('a');
    label.addEventListener('click', function (e) {
      var onCaret = e.target.classList && e.target.classList.contains('caret');
      if (onCaret || !hasLink) {
        e.preventDefault();
        dir.classList.toggle('collapsed');
      }
    });
  });

  // 点侧栏链接后关掉移动端抽屉
  document.querySelectorAll('#tree a').forEach(function (a) {
    a.addEventListener('click', function () { document.body.classList.remove('nav-open'); });
  });

  document.addEventListener('keydown', function (e) {
    if ((e.ctrlKey || e.metaKey) && (e.key === 'b' || e.key === 'B')) {
      e.preventDefault();
      setNav(!collapsed());
    }
  });

  var copy = document.getElementById('copyLink');
  if (copy) copy.addEventListener('click', function () {
    var text = location.href;
    var done = function () {
      var old = copy.textContent;
      copy.textContent = '已复制';
      setTimeout(function () { copy.textContent = old; }, 1200);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () { /* ignore */ });
    } else {
      var ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy'); done(); } catch (e) { /* ignore */ }
      document.body.removeChild(ta);
    }
  });
})();
