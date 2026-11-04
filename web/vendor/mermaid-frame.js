/* mermaid 渲染沙箱：只接收 postMessage，渲染完把高度报给父页面。
   运行在 sandbox="allow-scripts" 的 iframe 里（无 same-origin），
   所以它拿不到父页面的 DOM、cookie 或存储。 */
(function () {
  var out = document.getElementById('out');

  function post(msg) {
    try { parent.postMessage(msg, '*'); } catch (e) { /* 父页面已关闭 */ }
  }

  function height() {
    var h = document.documentElement.scrollHeight || document.body.scrollHeight || 0;
    return Math.max(h, 40);
  }

  if (!window.mermaid) {
    post({ type: 'error', message: 'mermaid 脚本未加载' });
    return;
  }

  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    theme: 'base',
    themeVariables: {
      darkMode: true,
      background: '#0d1424',
      primaryColor: '#1b2a45',
      primaryTextColor: '#e2e8f0',
      primaryBorderColor: '#2f4a72',
      lineColor: '#64748b',
      secondaryColor: '#111827',
      tertiaryColor: '#0f172a',
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: '13px'
    },
    flowchart: { useMaxWidth: false, htmlLabels: false },
    sequence: { useMaxWidth: false },
    gantt: { useMaxWidth: false },
    journey: { useMaxWidth: false },
    class: { useMaxWidth: false },
    state: { useMaxWidth: false },
    er: { useMaxWidth: false }
  });

  function fail(err) {
    var msg = String((err && err.message) || err || '未知错误');
    out.innerHTML = '';
    var pre = document.createElement('pre');
    pre.className = 'err';
    pre.textContent = msg;
    out.appendChild(pre);
    post({ type: 'error', message: msg, height: height() });
  }

  function done(svg) {
    out.innerHTML = svg;
    post({ type: 'done', height: height() });
  }

  function render(code) {
    var id = 'mmd' + Math.random().toString(36).slice(2);
    out.innerHTML = '';
    var ret;
    try {
      ret = mermaid.render(id, code);
    } catch (e) {
      fail(e);
      return;
    }
    if (ret && typeof ret.then === 'function') {
      ret.then(function (res) { done(res.svg); }).catch(fail);
    } else if (ret && ret.svg) {
      done(ret.svg);
    }
  }

  window.addEventListener('message', function (ev) {
    var d = ev.data || {};
    if (d.type === 'render' && typeof d.code === 'string') {
      render(d.code);
    }
  });

  if (window.ResizeObserver) {
    new ResizeObserver(function () { post({ type: 'resize', height: height() }); })
      .observe(document.documentElement);
  }

  post({ type: 'ready' });
})();
