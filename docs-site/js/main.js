/* dwxComment 文档站交互：主题切换、移动端导航、回顶、滚动动画、活跃导航 */
(function (window, document) {
  'use strict';

  /* ---------- 主题切换（auto / light / dark，localStorage 记忆） ---------- */
  var rootEl = document.documentElement;
  var themeBtn = document.getElementById('theme-toggle');

  function applyTheme(theme) {
    if (theme === 'light' || theme === 'dark') {
      rootEl.setAttribute('data-theme', theme);
    } else {
      rootEl.removeAttribute('data-theme');
    }
    if (themeBtn) {
      themeBtn.setAttribute('aria-label', theme === 'auto' ? '主题：跟随系统，点击切换' : '主题：' + theme + '，点击切换');
    }
  }

  function initTheme() {
    var saved = null;
    try { saved = localStorage.getItem('dwx-site-theme'); } catch (e) { /* ignore */ }
    applyTheme(saved || 'auto');
  }

  if (themeBtn) {
    themeBtn.addEventListener('click', function () {
      var cur = rootEl.getAttribute('data-theme') || 'auto';
      var next = cur === 'auto' ? 'light' : cur === 'light' ? 'dark' : 'auto';
      try { localStorage.setItem('dwx-site-theme', next); } catch (e) { /* ignore */ }
      applyTheme(next);
    });
  }

  /* ---------- 移动端导航菜单 ---------- */
  var navToggle = document.getElementById('nav-toggle');
  var navLinks = document.getElementById('nav-links');

  if (navToggle && navLinks) {
    navToggle.addEventListener('click', function () {
      navLinks.classList.toggle('open');
      navToggle.setAttribute('aria-expanded', navLinks.classList.contains('open'));
    });
    // 点击菜单项后收起
    navLinks.addEventListener('click', function (e) {
      if (e.target.tagName === 'A') {
        navLinks.classList.remove('open');
        navToggle.setAttribute('aria-expanded', 'false');
      }
    });
  }

  /* ---------- 滚动：回顶按钮 + 滚动动画 + 侧栏锚点偏移 ---------- */
  var backTop = document.getElementById('back-top');

  function onScroll() {
    var y = window.pageYOffset || document.documentElement.scrollTop;
    if (backTop) backTop.classList.toggle('show', y > 480);
    var reveals = document.querySelectorAll('.reveal');
    for (var i = 0; i < reveals.length; i++) {
      var el = reveals[i];
      if (el.classList.contains('in')) continue;
      var rect = el.getBoundingClientRect();
      if (rect.top < window.innerHeight - 40) el.classList.add('in');
    }
  }

  if (backTop) {
    backTop.addEventListener('click', function () {
      window.scrollTo({ top: 0, behavior: 'smooth' });
    });
  }

  var ticking = false;
  window.addEventListener('scroll', function () {
    if (!ticking) {
      window.requestAnimationFrame(function () { onScroll(); ticking = false; });
      ticking = true;
    }
  }, { passive: true });
  onScroll();

  /* ---------- 锚点平滑滚动时考虑固定导航高度 ---------- */
  document.querySelectorAll('a[href^="#"]').forEach(function (a) {
    a.addEventListener('click', function (e) {
      var id = this.getAttribute('href');
      if (id.length < 2) return;
      var target = document.querySelector(id);
      if (target) {
        e.preventDefault();
        var top = target.getBoundingClientRect().top + window.pageYOffset - 74;
        window.scrollTo({ top: top, behavior: 'smooth' });
        history.replaceState(null, '', id);
      }
    });
  });

  /* ---------- 表格行 hover 交互动画已由 CSS 完成，这里仅标记无 JS 时的兜底 ---------- */
  document.documentElement.classList.add('js');

  initTheme();
})(window, document);
