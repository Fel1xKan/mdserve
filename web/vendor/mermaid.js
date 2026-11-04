/* 把 <pre><code class="language-mermaid"> 换成沙箱 iframe。
   图表代码只通过 postMessage 传给 iframe，父页面不执行 mermaid 本身。 */
(function () {
  var meta = document.querySelector('meta[name="mdserve-base"]');
  var base = meta ? meta.getAttribute('content') : '';

  var codes = document.querySelectorAll(
    '#doc pre > code.language-mermaid, #doc pre > code.lang-mermaid'
  );
  if (!codes.length) return;

  Array.prototype.forEach.call(codes, function (code) {
    var pre = code.parentElement;
    var src = code.textContent || '';

    var block = document.createElement('div');
    block.className = 'mermaid-block';

    var wait = document.createElement('p');
    wait.className = 'mermaid-wait';
    wait.textContent = '正在渲染图表…';
    block.appendChild(wait);

    var frame = document.createElement('iframe');
    frame.setAttribute('sandbox', 'allow-scripts');
    frame.setAttribute('title', 'mermaid 图表');
    frame.setAttribute('loading', 'lazy');
    frame.src = base + '/-/mermaid';
    frame.style.width = '100%';
    frame.style.height = '160px';
    frame.style.border = '0';
    frame.style.display = 'block';
    block.appendChild(frame);

    pre.parentNode.insertBefore(block, pre);
    pre.style.display = 'none';

    var ready = false;
    var sent = false;

    function send() {
      if (!ready || sent) return;
      sent = true;
      try {
        frame.contentWindow.postMessage({ type: 'render', code: src }, '*');
      } catch (e) { /* iframe 尚未就绪，等下一次 load */ }
    }

    window.addEventListener('message', function (ev) {
      if (ev.source !== frame.contentWindow) return;
      var d = ev.data || {};

      if (d.type === 'ready') {
        ready = true;
        send();
        return;
      }
      if (d.type === 'resize' && d.height) {
        frame.style.height = d.height + 'px';
        return;
      }
      if (d.type === 'done') {
        block.classList.add('is-ready');
        if (wait.parentNode) wait.parentNode.removeChild(wait);
        if (d.height) frame.style.height = d.height + 'px';
        return;
      }
      if (d.type === 'error') {
        block.classList.add('is-error');
        if (wait.parentNode) wait.parentNode.removeChild(wait);

        var err = document.createElement('p');
        err.className = 'mermaid-err';
        err.textContent = '图表渲染失败：' + (d.message || '未知错误');
        block.appendChild(err);

        var srcEl = document.createElement('pre');
        srcEl.className = 'mermaid-src';
        srcEl.textContent = src;
        block.appendChild(srcEl);

        if (d.height) frame.style.height = d.height + 'px';
      }
    });

    frame.addEventListener('load', function () {
      // load 可能早于 ready 消息；send 内部有幂等保护。
      setTimeout(send, 0);
    });
  });
})();
