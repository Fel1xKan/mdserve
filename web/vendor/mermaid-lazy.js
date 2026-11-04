/* 只在图表接近视口时才加载 mermaid 主包（3.3MB）。
   没有 IntersectionObserver 或没有图表时退回立即加载。 */
(function () {
  var self = document.currentScript;
  var src = self && self.getAttribute('data-mermaid');
  if (!src) return;

  var blocks = document.querySelectorAll(
    '#doc pre > code.language-mermaid, #doc pre > code.lang-mermaid'
  );
  if (!blocks.length) return;

  var loaded = false;
  function load() {
    if (loaded) return;
    loaded = true;
    var s = document.createElement('script');
    s.src = src;
    s.defer = true;
    document.head.appendChild(s);
  }

  if (!('IntersectionObserver' in window)) {
    load();
    return;
  }

  var io = new IntersectionObserver(function (entries) {
    for (var i = 0; i < entries.length; i++) {
      if (entries[i].isIntersecting) {
        io.disconnect();
        load();
        return;
      }
    }
  }, { rootMargin: '400px 0px' });

  Array.prototype.forEach.call(blocks, function (b) { io.observe(b); });
})();
