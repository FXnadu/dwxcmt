/*!
 * dwxComment 评论组件 v0.1.0
 * 原生 ES5 实现，零框架依赖。嵌入方式：
 *   <!-- 方式 1：手动指定页面 ID -->
 *   <div data-page-id="/post/hello.html" data-server="https://api.example.com"></div>
 *
 *   <!-- 方式 2：自动识别当前路径（推荐模板统一引入） -->
 *   <div id="dwx-comment" data-server="https://api.example.com"></div>
 *   <script>
 *     window.dwxComment = { server: 'https://api.example.com', site: 'default' };
 *   </script>
 *
 *   <script src="/comment/comment.js" defer></script>
 *   <link rel="stylesheet" href="/comment/comment.css">
 */
(function (window, document) {
  'use strict';

  var DEFAULTS = {
    server: '',            // API 地址，空 = 同源
    site: 'default',
    locale: 'zh-CN',
    pageSize: 10,
    sort: 'asc',
    maxDepth: 3,
    emojiPanel: true,
    darkMode: 'auto',      // auto | light | dark
    maxLength: 500         // 评论字数上限，与服务端 content_max_length 默认一致；配置接口返回后覆盖
  };

  var EMOJIS = ['😀', '😄', '😁', '😊', '🙂', '😉', '😍', '😘', '😜', '🤪', '😝', '😛', '🤔', '🤨', '😐', '😑', '😶', '🫢', '🤭', '🤫', '🤗', '🥰', '😋', '😎', '🥳', '🤩', '🥺', '😌', '😏', '😒', '😓', '😔', '🙃', '😭', '😱', '😤', '😢', '😡', '😠', '😬', '🥱', '😴', '😷', '🤒', '🤕', '🤢', '🤮', '🥵', '🥶', '😇', '😈', '👻', '💀', '👍', '👎', '👏', '🙏', '💪', '🤝', '✌️', '🤞', '🤟', '👌', '👋', '🤙', '❤️', '💔', '💕', '💖', '💯', '🎉', '🎊', '🔥', '✨', '⭐', '🌟', '💫', '💥', '🎯'];

  var IMG_EXT = /\.(jpe?g|png|gif|webp)(\?[^\s<>"']*)?$/i;

  var LIKE_ICON = '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M7 10v12"/><path d="M15 5.88 14 10h5.83a2 2 0 0 1 1.92 2.56l-2.33 8A2 2 0 0 1 17.5 22H4a2 2 0 0 1-2-2v-8a2 2 0 0 1 2-2h2.76a2 2 0 0 0 1.79-1.11L12 2h0a3.13 3.13 0 0 1 3 3.88Z"/></svg>';

  function likeLabel(count) {
    return LIKE_ICON + '<span>' + (count || 0) + '</span>';
  }

  /* ========== 工具函数 ========== */

  function extend(target, source) {
    for (var k in source) {
      if (Object.prototype.hasOwnProperty.call(source, k) && source[k] !== undefined) {
        target[k] = source[k];
      }
    }
    return target;
  }

  function escapeHtml(str) {
    if (str === null || str === undefined) return '';
    var div = document.createElement('div');
    div.textContent = String(str);
    return div.innerHTML;
  }

  function trim(str) {
    return str == null ? '' : String(str).replace(/^\s+|\s+$/g, '');
  }

  // 当容器未指定 data-page-id 时，默认使用当前页面路径作为评论分组标识
  function getPageId() {
    return window.location ? window.location.pathname : '';
  }

  function formatTime(ts) {
    if (!ts) return '';
    var d = new Date(ts * 1000);
    function pad(n) { return n < 10 ? '0' + n : '' + n; }
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + ' ' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  }

  function dispatch(el, name, detail) {
    var evt;
    if (typeof window.CustomEvent === 'function') {
      evt = new CustomEvent(name, { detail: detail });
    } else {
      evt = document.createEvent('CustomEvent');
      evt.initCustomEvent(name, false, false, detail);
    }
    el.dispatchEvent(evt);
  }

  /* ========== 头像：有邮箱由后端返回真实头像候选（Gravatar/Cravatar/QQ），全部失败生成字母头像 ========== */

  function hashString(str) {
    var h = 0;
    for (var i = 0; i < str.length; i++) {
      h = ((h << 5) - h + str.charCodeAt(i)) | 0;
    }
    return Math.abs(h);
  }

  function letterAvatar(nick) {
    var h = hashString(nick || '');
    var colors = ['#e74c3c', '#e67e22', '#f1c40f', '#2ecc71', '#3498db', '#9b59b6', '#16a085', '#e84393'];
    // 统一生成随机字母 A-Z 及随机颜色，同一昵称结果稳定
    var letter = String.fromCharCode(65 + (h % 26));
    return '<span class="lc-avatar-letter" style="color:' + colors[h % colors.length] + '">' + escapeHtml(letter) + '</span>';
  }

  /* ========== 图片渲染（先转义再替换，防注入） ========== */

  function imgTag(url) {
    if (!/^https?:\/\//i.test(url)) return escapeHtml(url);
    // url 来自 renderContent 已转义文本（引号/尖括号均已实体化），直接拼入属性值
    // 是安全的（HTML tokenizer 中实体不参与属性边界识别），且不会二次转义 &，
    // 否则含 & 参数（如 CDN 签名 URL）的图片会被双重转义导致加载失败。
    return '<img class="lc-img" src="' + url + '" alt="图片" loading="lazy" referrerpolicy="no-referrer">';
  }

  function renderContent(text) {
    var escaped = escapeHtml(text);
    // Markdown 图片语法 ![](url)
    escaped = escaped.replace(/!\[[^\]]*\]\((https?:\/\/[^)\s]+)\)/g, function (m, url) {
      return imgTag(url);
    });
    // 裸图片 URL（http(s) 且带图片扩展名）
    escaped = escaped.replace(/(^|[\s(>])(https?:\/\/[^\s<>"']+)/gi, function (m, pre, url) {
      if (IMG_EXT.test(url)) return pre + imgTag(url);
      return m;
    });
    // 换行
    escaped = escaped.replace(/\n/g, '<br>');
    return escaped;
  }

  /* ========== 请求（XHR，ES5 兼容） ========== */

  function request(cfg, opts) {
    var xhr = new XMLHttpRequest();
    xhr.open(opts.method || 'GET', cfg.server + opts.url, true);
    // 仅 POST/PUT 等带请求体的请求设置 Content-Type，避免 GET 触发 CORS 预检
    if (opts.data) xhr.setRequestHeader('Content-Type', 'application/json; charset=utf-8');
    xhr.onreadystatechange = function () {
      if (xhr.readyState !== 4) return;
      var res = null;
      try { res = JSON.parse(xhr.responseText); } catch (e) { /* ignore */ }
      if (xhr.status >= 200 && xhr.status < 300) {
        if (res && res.code === 0) {
          if (opts.success) opts.success(res);
        } else if (opts.error) {
          opts.error(res, xhr.status);
        }
      } else if (opts.error) {
        opts.error(res, xhr.status);
      }
    };
    xhr.send(opts.data ? JSON.stringify(opts.data) : null);
  }

  /* ========== 组件 ========== */

  function dwxComment(el) {
    this.el = el;
    // 优先级：容器 data-page-id > 当前页面路径 > 空
    this.pageId = trim(el.getAttribute('data-page-id')) || getPageId();
    this.cfg = extend(extend({}, DEFAULTS), window.dwxComment || {});
    // 容器 data-* 优先级最高
    var attrMap = {
      'data-server': 'server', 'data-site': 'site', 'data-locale': 'locale',
      'data-page-size': 'pageSize', 'data-sort': 'sort',
      'data-max-depth': 'maxDepth', 'data-emoji-panel': 'emojiPanel', 'data-dark-mode': 'darkMode'
    };
    for (var attr in attrMap) {
      if (el.hasAttribute(attr)) this.cfg[attrMap[attr]] = el.getAttribute(attr);
    }
    if (this.cfg.pageSize) this.cfg.pageSize = parseInt(this.cfg.pageSize, 10) || 10;
    if (this.cfg.maxDepth) this.cfg.maxDepth = parseInt(this.cfg.maxDepth, 10) || 3;

    this.page = 1;
    this.totalPages = 1;
    this.total = 0;
    this.replyingTo = null; // {id, nick, rootId}
    this.loading = false;
    this.moreCount = 0;
    this.isMobile = this.detectMobile(); // 移动端扁平回复渲染
    this.siteConfig = { adminBadge: '站长', adminAvatar: '', adminNick: '站长', maxLength: DEFAULTS.maxLength, pagerType: 'more' };

    this.build();
  }

  dwxComment.prototype.build = function () {
    var self = this;
    if (!this.pageId) {
      this.el.innerHTML = '<p class="lc-error">错误：无法确定页面标识，请填写 data-page-id 属性</p>';
      return;
    }
    var html =
      '<div class="lc-wrap">' +
        '<div class="lc-head">' +
          '<span class="lc-title">评论</span><span class="lc-count"></span>' +
          '<span class="lc-spacer"></span>' +
          '<div class="lc-sort" aria-label="排序方式">' +
            '<button type="button" class="lc-sort-btn" aria-haspopup="listbox" aria-expanded="false">' +
              '<span class="lc-sort-label">最新</span>' +
              '<svg class="lc-sort-arrow" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path fill="currentColor" d="M4 6l4 4 4-4z"/></svg>' +
            '</button>' +
            '<ul class="lc-sort-menu" role="listbox" hidden>' +
              '<li role="option" data-value="asc" tabindex="-1">最新</li>' +
              '<li role="option" data-value="desc" tabindex="-1">倒序</li>' +
              '<li role="option" data-value="hot" tabindex="-1">热度</li>' +
            '</ul>' +
          '</div>' +
        '</div>' +
        '<form class="lc-form" novalidate>' +
          '<div class="lc-form-row">' +
            '<input class="lc-input lc-nick" type="text" maxlength="20" placeholder="昵称 *">' +
            '<input class="lc-input lc-email" type="email" placeholder="邮箱（选填）">' +
            '<input class="lc-input lc-link" type="text" placeholder="网站（选填）">' +
          '</div>' +
          '<div class="lc-reply-bar" style="display:none"></div>' +
          '<textarea class="lc-textarea" rows="4" maxlength="' + this.siteConfig.maxLength + '" placeholder="写下你的评论…支持 Emoji 与图片 URL"></textarea>' +
          '<div class="lc-form-foot">' +
            '<span class="lc-toolbar">' +
              '<button type="button" class="lc-btn lc-emoji-btn">😊 表情</button>' +
              '<button type="button" class="lc-btn lc-clear-btn" style="display:none">清除信息</button>' +
            '</span>' +
            '<span class="lc-char-count">0/' + this.siteConfig.maxLength + '</span>' +
            '<button type="submit" class="lc-btn lc-submit-btn">发表评论</button>' +
          '</div>' +
          '<div class="lc-emoji-panel" style="display:none"></div>' +
        '</form>' +
        '<div class="lc-list"></div>' +
        '<div class="lc-pager" style="display:none"></div>' +
        '<div class="lc-toast" style="display:none"></div>' +
      '</div>';
    this.el.innerHTML = html;

    this.listEl = this.el.querySelector('.lc-list');
    this.pagerEl = this.el.querySelector('.lc-pager');
    this.countEl = this.el.querySelector('.lc-count');
    this.sortEl = this.el.querySelector('.lc-sort');
    this.sortBtnEl = this.el.querySelector('.lc-sort-btn');
    this.sortLabelEl = this.el.querySelector('.lc-sort-label');
    this.sortMenuEl = this.el.querySelector('.lc-sort-menu');
    this.formEl = this.el.querySelector('.lc-form');

    this.sort = this.cfg.sort || 'asc';
    if (this.sortEl) this.setSortLabel();
    this.nickEl = this.el.querySelector('.lc-nick');
    this.emailEl = this.el.querySelector('.lc-email');
    this.linkEl = this.el.querySelector('.lc-link');
    this.textEl = this.el.querySelector('.lc-textarea');
    this.replyBarEl = this.el.querySelector('.lc-reply-bar');
    this.toastEl = this.el.querySelector('.lc-toast');
    this.charCountEl = this.el.querySelector('.lc-char-count');
    this.clearBtnEl = this.el.querySelector('.lc-clear-btn');

    this.bindEvents();
    this.restoreUser();
    this.loadSiteConfig();
    this.loadPage(1);
    this.loadCount();
    if (this.cfg.emojiPanel !== false && this.cfg.emojiPanel !== 'false') {
      this.buildEmojiPanel();
    }
    this.watchBreakpoint();
    dispatch(this.el, 'lc:ready', {});
  };

  /* ========== 移动端适配（640px 以下扁平回复，对齐 B 站移动端） ========== */

  dwxComment.prototype.detectMobile = function () {
    var mql = window.matchMedia ? window.matchMedia('(max-width: 640px)') : null;
    return !mql || mql.matches;
  };

  // 监听断点切换：跨断点时用已加载数据重渲染（不重新请求），桌面嵌套 ↔ 移动扁平互切
  dwxComment.prototype.watchBreakpoint = function () {
    var self = this;
    var mql = window.matchMedia ? window.matchMedia('(max-width: 640px)') : null;
    if (!mql) return;
    var handler = function (e) {
      var next = e.matches;
      if (next !== self.isMobile) {
        self.isMobile = next;
        self.rerender();
      }
    };
    if (mql.addEventListener) mql.addEventListener('change', handler);
    else if (mql.addListener) mql.addListener(handler);
  };

  // 用最近一次加载的数据重渲染当前页
  dwxComment.prototype.rerender = function () {
    if (!this._lastData) return;
    this.listEl.innerHTML = '';
    // 重渲染只包含最近一页数据，同步重置累积映射与展开标记，避免与 DOM 不一致
    this.commentById = {};
    this._expandedForHash = false;
    this.renderList(this._lastData.roots || [], this._lastData.children || []);
    this.updateMoreBtn();
    this.maybeScrollToHash();
  };

  // 同步自定义排序下拉的按钮文本与选项选中态
  dwxComment.prototype.setSortLabel = function () {
    var opts = this.sortMenuEl.querySelectorAll('[role="option"]');
    for (var i = 0; i < opts.length; i++) {
      if (opts[i].getAttribute('data-value') === this.sort) {
        opts[i].setAttribute('aria-selected', 'true');
        this.sortLabelEl.textContent = opts[i].textContent;
      } else {
        opts[i].removeAttribute('aria-selected');
      }
    }
  };

  // 打开排序下拉
  dwxComment.prototype.openSortMenu = function () {
    // 与每页条数下拉互斥：同一时间只展开一个
    if (this.sizeMenuEl && !this.sizeMenuEl.hidden) {
      this.sizeMenuEl.hidden = true;
      this.sizeBtnEl.setAttribute('aria-expanded', 'false');
      this.sizeWrapEl.classList.remove('lc-open');
    }
    this.sortMenuEl.hidden = false;
    this.sortBtnEl.setAttribute('aria-expanded', 'true');
    this.sortEl.classList.add('lc-open');
  };

  // 关闭排序下拉
  dwxComment.prototype.closeSortMenu = function () {
    this.sortMenuEl.hidden = true;
    this.sortBtnEl.setAttribute('aria-expanded', 'false');
    this.sortEl.classList.remove('lc-open');
  };

  // 键盘导航：返回当前选项索引（无选中时以首个为基准）
  dwxComment.prototype.sortOptions = function () {
    return this.sortMenuEl.querySelectorAll('[role="option"]');
  };

  dwxComment.prototype.bindEvents = function () {
    var self = this;

    // 排序切换（自定义下拉）
    if (this.sortEl) {
      this.sortBtnEl.addEventListener('click', function (e) {
        e.stopPropagation();
        if (self.sortMenuEl.hidden) {
          self.openSortMenu();
        } else {
          self.closeSortMenu();
        }
      });
      // 键盘打开菜单并聚焦当前/首项
      this.sortBtnEl.addEventListener('keydown', function (e) {
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          var opts = self.sortOptions();
          var cur = self.sortMenuEl.querySelector('[aria-selected="true"]');
          var idx = Array.prototype.indexOf.call(opts, cur);
          if (self.sortMenuEl.hidden) self.openSortMenu();
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            idx = e.key === 'ArrowDown'
              ? (idx + 1) % opts.length
              : (idx - 1 + opts.length) % opts.length;
            opts[idx].focus();
          } else if (idx >= 0) {
            opts[idx].focus();
          }
        }
      });
      // 菜单内方向键/Home/End/Enter/Space 选择，Esc 关闭
      this.sortMenuEl.addEventListener('keydown', function (e) {
        var opts = self.sortOptions();
        var idx = Array.prototype.indexOf.call(opts, e.target);
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault();
          idx = e.key === 'ArrowDown'
            ? (idx + 1) % opts.length
            : (idx - 1 + opts.length) % opts.length;
          opts[idx].focus();
        } else if (e.key === 'Home' || e.key === 'End') {
          e.preventDefault();
          (e.key === 'Home' ? opts[0] : opts[opts.length - 1]).focus();
        } else if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          self.sort = e.target.getAttribute('data-value');
          self.setSortLabel();
          self.closeSortMenu();
          self.loadPage(1);
        } else if (e.key === 'Escape') {
          self.closeSortMenu();
          self.sortBtnEl.focus();
        }
      });
      this.sortMenuEl.addEventListener('click', function (e) {
        var opt = e.target.closest('[role="option"]');
        if (!opt) return;
        self.sort = opt.getAttribute('data-value');
        self.setSortLabel();
        self.closeSortMenu();
        self.loadPage(1);
      });
      // 点击外部关闭
      document.addEventListener('click', function (e) {
        if (!self.sortEl.contains(e.target) && !self.sortMenuEl.hidden) {
          self.closeSortMenu();
        }
      });
      // Esc 关闭
      document.addEventListener('keydown', function (e) {
        if (e.key === 'Escape' && !self.sortMenuEl.hidden) {
          self.closeSortMenu();
          self.sortBtnEl.focus();
        }
      });
    }

    // 字数统计
    this.textEl.addEventListener('input', function () {
      self.charCountEl.textContent = self.textEl.value.length + '/' + self.siteConfig.maxLength;
    });

    // 表情面板
    var emojiBtn = this.el.querySelector('.lc-emoji-btn');
    var emojiPanel = this.el.querySelector('.lc-emoji-panel');
    emojiBtn.addEventListener('click', function (e) {
      e.stopPropagation();
      emojiPanel.style.display = emojiPanel.style.display === 'none' ? 'flex' : 'none';
    });
    // 点击面板外部关闭
    document.addEventListener('click', function (e) {
      if (!emojiPanel.contains(e.target) && e.target !== emojiBtn) {
        emojiPanel.style.display = 'none';
      }
    });

    // 表单提交
    this.formEl.addEventListener('submit', function (e) {
      e.preventDefault();
      self.submit();
    });

    // 清除记忆
    this.clearBtnEl.addEventListener('click', function () {
      try { localStorage.removeItem('lc_user'); } catch (e) { /* ignore */ }
      self.nickEl.value = ''; self.emailEl.value = ''; self.linkEl.value = '';
      self.clearBtnEl.style.display = 'none';
      self.toast('已清除个人信息', 'info');
    });

    // 暗黑模式手动切换（属性覆盖系统偏好）。
    // 注意：data-theme 必须设置在 .lc-wrap 上（CSS 选择器为 .lc-wrap[data-theme=...]），
    // 而不是挂载容器上，否则覆盖无效。
    var wrap = this.el.querySelector('.lc-wrap');
    if (wrap) {
      if (this.cfg.darkMode === 'light') wrap.setAttribute('data-theme', 'light');
      else if (this.cfg.darkMode === 'dark') wrap.setAttribute('data-theme', 'dark');
    }
  };

  dwxComment.prototype.buildEmojiPanel = function () {
    var panel = this.el.querySelector('.lc-emoji-panel');
    for (var i = 0; i < EMOJIS.length; i++) {
      (function (emoji) {
        var b = document.createElement('button');
        b.type = 'button';
        b.className = 'lc-emoji';
        b.textContent = emoji;
        b.addEventListener('click', function () {
          insertAtCursor(this);
          panel.style.display = 'none';
        });
        panel.appendChild(b);
      })(EMOJIS[i]);
    }
    var self = this;
    function insertAtCursor(btn) {
      var ta = self.textEl;
      var start = ta.selectionStart || ta.value.length;
      ta.value = ta.value.slice(0, start) + btn.textContent + ta.value.slice(ta.selectionEnd || start);
      ta.focus();
      ta.selectionStart = ta.selectionEnd = start + btn.textContent.length;
      self.charCountEl.textContent = ta.value.length + '/' + self.siteConfig.maxLength;
    }
  };

  /* ========== 数据加载 ========== */

  // 加载站点配置（站长徽章文案 / 站长头像），默认「站长」+ 无头像
  dwxComment.prototype.loadSiteConfig = function () {
    var self = this;
    request(this.cfg, {
      url: '/api/v1/site-config?site=' + encodeURIComponent(this.cfg.site),
      success: function (res) {
        if (!res.data) return;
        self.siteConfig.adminBadge = res.data.adminBadge || '站长';
        self.siteConfig.adminAvatar = res.data.adminAvatar || '';
        self.siteConfig.adminNick = res.data.adminNick || '站长';
        // 评论区翻页方式：more（加载更多）/ pages（页码分页），由后台站点设置下发
        self.siteConfig.pagerType = (res.data.pagerType === 'pages') ? 'pages' : 'more';
        // 评论字数上限由后端配置下发，保证与 content_max_length 一致
        var maxLen = parseInt(res.data.contentMaxLength, 10);
        if (maxLen > 0) {
          self.siteConfig.maxLength = maxLen;
          if (self.textEl) self.textEl.maxLength = maxLen;
          if (self.charCountEl) self.charCountEl.textContent = '0/' + maxLen;
        }
        // 配置到达后重渲染当前页，应用自定义昵称/徽章/头像
        self.loadPage(self.page);
      }
    });
  };

  dwxComment.prototype.loadCount = function () {
    var self = this;
    request(this.cfg, {
      url: '/api/v1/comments/count?pageId=' + encodeURIComponent(this.pageId) + '&site=' + encodeURIComponent(this.cfg.site),
      success: function (res) {
        self.total = res.data && res.data.count ? res.data.count : 0;
        if (self.countEl) self.countEl.textContent = '(' + self.total + ')';
        dispatch(self.el, 'lc:count', { count: self.total });
      }
    });
  };

  dwxComment.prototype.loadPage = function (page) {
    var self = this;
    if (this.loading) return;
    this.loading = true;
    var url = '/api/v1/comments?pageId=' + encodeURIComponent(this.pageId) +
      '&site=' + encodeURIComponent(this.cfg.site) +
      '&page=' + page +
      '&pageSize=' + this.cfg.pageSize +
      '&sort=' + this.sort;
    request(this.cfg, {
      url: url,
      success: function (res) {
        self.loading = false;
        self.page = page;
        self.total = res.data.total;
        self.totalPages = res.data.totalPages || 1;
        self.moreCount = self.total - self.page * self.cfg.pageSize;
        // 页码分页模式：每次切页替换列表；加载更多模式：仅首页清空
        if (page === 1 || self.siteConfig.pagerType === 'pages') {
          self.listEl.innerHTML = '';
          self.countEl.textContent = '(' + self.total + ')';
          // 列表整体替换时重置跨页累积的评论映射与折叠展开标记，
          // 避免上一次排序/页面的数据残留（nick 回显、折叠判断都依赖它）
          self.commentById = {};
          self._expandedForHash = false;
        }
        self._lastData = { roots: res.data.roots || [], children: res.data.children || [] };
        self.renderList(res.data.roots || [], res.data.children || []);
        self.updatePager();
        self.maybeScrollToHash();
        dispatch(self.el, 'lc:loaded', { page: page });
      },
      error: function (res) {
        self.loading = false;
        self.toast((res && res.msg) || '加载评论失败', 'error');
        dispatch(self.el, 'lc:error', { msg: (res && res.msg) || '加载失败' });
      }
    });
  };

  // 统一分页渲染入口：按站点配置（后台设置）分发到「加载更多」或「页码分页」
  dwxComment.prototype.updatePager = function () {
    this.pagerEl.innerHTML = '';
    if (this.siteConfig.pagerType === 'pages') {
      this.renderPagesPager();
    } else {
      this.updateMoreBtn();
    }
  };

  dwxComment.prototype.updateMoreBtn = function () {
    var self = this;
    this.pagerEl.innerHTML = '';
    this.pagerEl.style.display = 'none';
    if (this.page >= this.totalPages) return;
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'lc-btn lc-more-btn';
    btn.textContent = this.moreCount > 0 ? '加载更多（剩余 ' + this.moreCount + ' 条）' : '加载更多';
    btn.addEventListener('click', function () {
      self.loadPage(self.page + 1);
    });
    this.pagerEl.appendChild(btn);
    this.pagerEl.style.display = 'block';
  };

  // 页码分页器：窗口化页码（首尾固定+省略号收拢，任意总页数不溢出）+ 每页条数选择 + 跳至指定页
  dwxComment.prototype.renderPagesPager = function () {
    var self = this;
    var el = this.pagerEl;
    el.style.display = 'none';
    if (this.totalPages <= 1) return;
    var page = this.page, total = this.totalPages;

    // 页码窗口：当前页±2，首尾固定，间隔用省略号收拢；100 页也只需 7 个页码位
    var pages = [];
    var start = Math.max(1, page - 2), end = Math.min(total, page + 2);
    if (start > 1) pages.push(1);
    if (start > 2) pages.push('…');
    for (var p = start; p <= end; p++) pages.push(p);
    if (end < total - 1) pages.push('…');
    if (end < total) pages.push(total);

    var pagesHtml = '';
    for (var i = 0; i < pages.length; i++) {
      if (pages[i] === '…') {
        pagesHtml += '<span class="lc-pager-ellipsis">…</span>';
      } else {
        pagesHtml += '<button type="button" class="lc-pager-btn' + (pages[i] === page ? ' active' : '') + '" data-p="' + pages[i] + '">' + pages[i] + '</button>';
      }
    }

    // 每页条数选项：预设 10/20/50/100，容器自定义 data-page-size 不在其中时动态补充
    var sizeOptions = [10, 20, 50, 100];
    if (sizeOptions.indexOf(this.cfg.pageSize) === -1) sizeOptions.push(this.cfg.pageSize);
    sizeOptions.sort(function (a, b) { return a - b; });
    var sizeItems = '';
    for (var si = 0; si < sizeOptions.length; si++) {
      sizeItems += '<li role="option" data-value="' + sizeOptions[si] + '"' +
        (sizeOptions[si] === this.cfg.pageSize ? ' aria-selected="true"' : '') +
        ' tabindex="-1">每页' + sizeOptions[si] + '条</li>';
    }

    el.innerHTML = '<div class="lc-pager-bar">' +
      '<div class="lc-sort lc-pager-size-wrap" aria-label="每页条数">' +
        '<button type="button" class="lc-sort-btn lc-pager-size-btn" aria-haspopup="listbox" aria-expanded="false">' +
          '<span class="lc-sort-label lc-pager-size-label">每页' + this.cfg.pageSize + '条</span>' +
          '<svg class="lc-sort-arrow" viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path fill="currentColor" d="M4 6l4 4 4-4z"/></svg>' +
        '</button>' +
        '<ul class="lc-sort-menu lc-pager-size-menu" role="listbox" hidden>' + sizeItems + '</ul>' +
      '</div>' +
      '<div class="lc-pager-nav">' +
        '<button type="button" class="lc-pager-btn lc-pager-prev" data-p="' + (page > 1 ? page - 1 : '') + '"' + (page <= 1 ? ' disabled' : '') + '>上一页</button>' +
        '<div class="lc-pager-pages">' + pagesHtml + '</div>' +
        '<button type="button" class="lc-pager-btn lc-pager-next" data-p="' + (page < total ? page + 1 : '') + '"' + (page >= total ? ' disabled' : '') + '>下一页</button>' +
        '<span class="lc-pager-info">第 ' + page + '/' + total + ' 页</span>' +
        '<span class="lc-pager-goto">跳至 <input type="number" class="lc-pager-input" min="1" max="' + total + '" value="' + page + '" aria-label="跳转页码"><button type="button" class="lc-pager-btn lc-pager-go">跳转</button></span>' +
      '</div>' +
    '</div>';

    // 每页条数下拉（自定义，与排序方式同款样式）
    this.sizeWrapEl = el.querySelector('.lc-pager-size-wrap');
    this.sizeBtnEl = el.querySelector('.lc-pager-size-btn');
    this.sizeLabelEl = el.querySelector('.lc-pager-size-label');
    this.sizeMenuEl = el.querySelector('.lc-pager-size-menu');
    var sizeWrap = this.sizeWrapEl, sizeBtn = this.sizeBtnEl, sizeMenu = this.sizeMenuEl;

    // 页面内点击关闭：先移除上次渲染绑定的监听，避免重复累积
    if (this.onDocClickForSize) {
      document.removeEventListener('click', this.onDocClickForSize);
    }
    this.onDocClickForSize = function (ev) {
      if (sizeWrap.contains(ev.target)) return;
      if (!sizeMenu.hidden) {
        sizeMenu.hidden = true;
        sizeBtn.setAttribute('aria-expanded', 'false');
        sizeWrap.classList.remove('lc-open');
      }
    };
    document.addEventListener('click', this.onDocClickForSize);

    function closeSizeMenu() {
      sizeMenu.hidden = true;
      sizeBtn.setAttribute('aria-expanded', 'false');
      sizeWrap.classList.remove('lc-open');
    }
    sizeBtn.addEventListener('click', function (e) {
      e.stopPropagation();
      if (sizeMenu.hidden) {
        if (self.sortMenuEl && !self.sortMenuEl.hidden) self.closeSortMenu();
        sizeMenu.hidden = false;
        sizeBtn.setAttribute('aria-expanded', 'true');
        sizeWrap.classList.add('lc-open');
      } else {
        closeSizeMenu();
      }
    });
    sizeMenu.addEventListener('click', function (e) {
      var li = e.target.closest ? e.target.closest('li[role="option"]') : null;
      if (!li) return;
      var v = parseInt(li.getAttribute('data-value'), 10);
      closeSizeMenu();
      if (!v || v === self.cfg.pageSize) return;
      self.cfg.pageSize = v;
      self.loadPage(1);
    });
    sizeMenu.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') { e.preventDefault(); closeSizeMenu(); sizeBtn.focus(); }
    });

    // 页码/上一页/下一页 点击（事件委托）
    el.onclick = function (ev) {
      var t = ev.target;
      if (t.tagName !== 'BUTTON' || t.disabled) return;
      var p = parseInt(t.getAttribute('data-p'), 10);
      if (!p || p === self.page) return;
      self.loadPage(p);
    };

    // 跳至指定页：回车或点击跳转按钮
    var input = el.querySelector('.lc-pager-input');
    var goBtn = el.querySelector('.lc-pager-go');
    function gotoPage() {
      var v = parseInt(input.value, 10);
      if (!v || v < 1 || v > self.totalPages || v === self.page) return;
      self.loadPage(v);
    }
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); gotoPage(); }
    });
    goBtn.addEventListener('click', gotoPage);

    el.style.display = 'block';
  };

  // 邮件链接带 #comment-{id} 锚点时：滚动到目标评论并短暂高亮。
  // 定位顺序：直接命中 → 已加载但被折叠（超过 maxDepth 展开层数 / 移动端回复收拢）则展开重试
  // → 不在已加载页则自动继续加载后续页 → 全部加载完仍无目标则 toast 提示。
  dwxComment.prototype.maybeScrollToHash = function () {
    var self = this;
    if (self.hashDone) return;
    var m = /^#comment-(\d+)$/.exec(location.hash);
    if (!m) return;
    var id = m[1];
    var el = self.listEl.querySelector('#comment-' + id);
    if (el) {
      self.finishHashJump(el);
      return;
    }
    // 数据里已存在（该页已加载）但节点被折叠：展开折叠区后重试（每次跳转只尝试一次）。
    // 折叠可能嵌套（展开一层后出现新的折叠按钮），循环点击直到不再有可展开的折叠区；
    // 展开后仍找不到时不要提前 return，落到下方「翻页 / toast」分支。
    if (self.commentById && self.commentById[String(id)] && !self._expandedForHash) {
      self._expandedForHash = true;
      var el2 = null;
      for (;;) {
        var btns = self.listEl.querySelectorAll('.lc-collapse-btn');
        var clicked = false;
        for (var i = 0; i < btns.length; i++) {
          // 移动端「收起回复」按钮点击会收起已展开的回复，跳过以免误收
          if (btns[i].textContent === '收起回复') continue;
          btns[i].click();
          clicked = true;
        }
        if (!clicked) break; // 没有更多可展开的折叠区
        el2 = self.listEl.querySelector('#comment-' + id);
        if (el2) break;
      }
      if (el2) {
        self.finishHashJump(el2);
        return;
      }
    }
    if (self.page < self.totalPages) {
      self.loadPage(self.page + 1);
    } else {
      self.hashDone = true; // 全部加载完仍无目标，可能已被删除
      self.toast('未找到该评论', 'error');
    }
  };

  // 锚点命中：平滑滚动到窗口垂直中间，滚动结束（scrollend）后再加高亮播放闪烁。
  // 若在滚动前加类，CSS 动画 2s 从加类即开播，长页面下最强的开头段会在屏幕外播完，
  // 用户滚到评论时动画已近尾声，观感像「没反应」。
  dwxComment.prototype.finishHashJump = function (el) {
    this.hashDone = true;
    var currentY = window.pageYOffset || document.documentElement.scrollTop;
    // 精确滚动到窗口垂直中间。scrollIntoView 的 block:'center' 在评论接近
    // 文档底部时会被底部空间限制而偏移，这里手动计算目标滚动位置。
    var targetY = el.getBoundingClientRect().top + currentY -
      (window.innerHeight - el.offsetHeight) / 2;
    targetY = Math.max(0, targetY);

    var played = false;
    function play() {
      if (played) return;
      played = true;
      el.classList.add('lc-highlight');
    }

    // 目标已在窗口中部、无需滚动：scrollend 不会触发，直接播放
    if (Math.abs(targetY - currentY) < 2) {
      play();
      return;
    }

    window.scrollTo({ top: targetY, behavior: 'smooth' });

    if ('onscrollend' in document) {
      // 滚动真正停下后才播放。兜底超时必须明显大于一次平滑滚动，
      // 否则长页面上会抢在 scrollend 前播放，问题依旧
      document.addEventListener('scrollend', play, { once: true });
      setTimeout(play, 3000);
    } else {
      // 不支持 scrollend：固定延迟播放（总比不播强）
      setTimeout(play, 800);
    }
  };

  /* ========== 渲染 ========== */

  dwxComment.prototype.renderList = function (roots, children) {
    var self = this;
    // 子评论按 parentId 分组
    var childrenMap = {};
    for (var i = 0; i < children.length; i++) {
      var c = children[i];
      var pid = String(c.parentId || 0);
      if (!childrenMap[pid]) childrenMap[pid] = [];
      childrenMap[pid].push(c);
    }
    // 父级 ID → 子评论列表（含跨页根评论）
    self.childrenMap = childrenMap;
    // 根评论 ID → 该根下全部后代回复（后端已按时间正序，直接过滤保序）——移动端扁平渲染用
    var rootChildren = {};
    for (var r2 = 0; r2 < children.length; r2++) {
      var rc = children[r2];
      var rid = String(rc.rootId || 0);
      if (!rootChildren[rid]) rootChildren[rid] = [];
      rootChildren[rid].push(rc);
    }
    self.rootChildrenMap = rootChildren;
    // 全部评论 id → 评论，用于回复时显示被回复者昵称；
    // 跨页累积（翻页时合并进已有映射），供锚点定位判断「已加载但被折叠」的评论
    var commentById = self.commentById || {};
    for (var r = 0; r < roots.length; r++) {
      commentById[String(roots[r].id)] = roots[r];
    }
    for (var k = 0; k < children.length; k++) {
      commentById[String(children[k].id)] = children[k];
    }
    self.commentById = commentById;

    for (var j = 0; j < roots.length; j++) {
      var node = this.createCommentNode(roots[j], 1, roots[j].parentId || 0);
      this.listEl.appendChild(node);
    }
    if (roots.length === 0 && this.page === 1) {
      this.listEl.innerHTML = '<p class="lc-empty">还没有评论，来抢沙发吧~</p>';
    }
  };

  dwxComment.prototype.createCommentNode = function (comment, depth, parentId, isFlat) {
    var self = this;
    var wrap = document.createElement('div');
    wrap.className = isFlat ? 'lc-comment lc-reply-flat' : 'lc-comment';
    wrap.setAttribute('data-id', comment.id);
    // 锚点 id：邮件链接可定位到具体评论
    wrap.id = 'comment-' + comment.id;

    // 头像候选：站长头像 → 后端返回的真实头像（Gravatar/Cravatar/QQ）→ 字母头像兜底
    var avatar = document.createElement('div');
    avatar.className = 'lc-avatar';
    var candidates = [];
    if (comment.isAdmin && this.siteConfig.adminAvatar) candidates.push(this.siteConfig.adminAvatar);
    if (comment.avatarUrls && comment.avatarUrls.length) {
      for (var k = 0; k < comment.avatarUrls.length; k++) {
        var u = comment.avatarUrls[k];
        // 相对路径（QQ 头像代理地址）补上 API 服务器前缀，兼容跨域部署
        if (!/^https?:\/\//i.test(u)) u = this.cfg.server + u;
        candidates.push(u);
      }
    }
    if (candidates.length > 0) {
      var img = document.createElement('img');
      img.alt = '';
      img.className = 'lc-avatar-img';
      var idx = 0;
      var done = false;
      var timer = null;
      // 单个候选加载超时（毫秒）：第三方服务（如 cravatar.cn）国内偶发挂起，
      // 既不成功也不报错，超时后强制跳到下一个候选，避免把后续头像堵死
      var AVATAR_TIMEOUT = 1500;
      function avatarNext() {
        clearTimeout(timer);
        if (done) return;
        idx++;
        if (idx < candidates.length) {
          img.src = candidates[idx];
          timer = setTimeout(avatarNext, AVATAR_TIMEOUT);
        } else {
          done = true;
          avatar.innerHTML = letterAvatar(comment.nick);
        }
      }
      img.onload = function () { clearTimeout(timer); done = true; };
      // 逐个加载候选头像，全部失败/超时才回退字母头像
      img.onerror = avatarNext;
      img.src = candidates[0];
      timer = setTimeout(avatarNext, AVATAR_TIMEOUT);
      avatar.appendChild(img);
    } else {
      avatar.innerHTML = letterAvatar(comment.nick);
    }

    // 主体
    var body = document.createElement('div');
    body.className = 'lc-body';

    // meta
    var meta = document.createElement('div');
    meta.className = 'lc-meta';
    var nickEl;
    var displayNick = comment.isAdmin ? (this.siteConfig.adminNick || '站长') : comment.nick;
    if (comment.link) {
      var a = document.createElement('a');
      a.href = comment.link;
      a.target = '_blank';
      a.rel = 'noopener nofollow ugc';
      a.className = 'lc-nick';
      a.textContent = displayNick;
      nickEl = a;
    } else {
      nickEl = document.createElement('span');
      nickEl.className = 'lc-nick';
      nickEl.textContent = displayNick;
    }
    meta.appendChild(nickEl);
    if (comment.isAdmin) {
      var admin = document.createElement('span');
      admin.className = 'lc-admin';
      admin.textContent = this.siteConfig.adminBadge || '站长';
      meta.appendChild(admin);
    }
    if (comment.parentId) {
      var atWrap = document.createElement('span');
      atWrap.className = 'lc-at';
      var parent = this.commentById ? this.commentById[String(comment.parentId)] : null;
      atWrap.textContent = parent && parent.nick ? ' 回复 ' + parent.nick : ' 回复';
      meta.appendChild(atWrap);
    }
    if (comment.isPinned) {
      var pin = document.createElement('span');
      pin.className = 'lc-pin';
      pin.textContent = '置顶';
      meta.appendChild(pin);
    }
    var time = document.createElement('span');
    time.className = 'lc-time';
    time.textContent = formatTime(comment.createTime);
    meta.appendChild(time);

    // 内容
    var content = document.createElement('div');
    content.className = 'lc-content';
    content.innerHTML = renderContent(comment.content);

    // 操作
    var actions = document.createElement('div');
    actions.className = 'lc-actions';
    var likeBtn = document.createElement('button');
    likeBtn.type = 'button';
    likeBtn.className = 'lc-like';
    likeBtn.innerHTML = likeLabel(comment.likeCount);
    likeBtn.addEventListener('click', function () {
      self.like(comment.id, likeBtn);
    });
    var replyBtn = document.createElement('button');
    replyBtn.type = 'button';
    replyBtn.className = 'lc-reply';
    replyBtn.textContent = '回复';
    replyBtn.addEventListener('click', function () {
      self.startReply(comment);
    });
    actions.appendChild(likeBtn);
    actions.appendChild(replyBtn);

    body.appendChild(meta);
    body.appendChild(content);
    body.appendChild(actions);

    // 子评论：桌面端递归嵌套（maxDepth 层以上折叠）；移动端在根评论下扁平平铺（对齐 B 站移动端）
    if (this.isMobile) {
      if (depth === 1) {
        var flat = this.rootChildrenMap ? this.rootChildrenMap[String(comment.id)] : null;
        if (flat && flat.length) {
          body.appendChild(this.buildFlatReplies(flat));
        }
      }
      // 移动端回复不递归，不会出现 depth > 1 的节点
    } else {
      var children = this.childrenMap ? this.childrenMap[String(comment.id)] : null;
      if (children && children.length) {
        if (depth < this.cfg.maxDepth) {
          var childWrap = document.createElement('div');
          childWrap.className = 'lc-children';
          for (var i = 0; i < children.length; i++) {
            childWrap.appendChild(this.createCommentNode(children[i], depth + 1, comment.id));
          }
          body.appendChild(childWrap);
        } else {
          // 超过默认展开层数：折叠
          var more = document.createElement('button');
          more.type = 'button';
          more.className = 'lc-collapse-btn';
          more.textContent = '展开更多回复（' + children.length + '）';
          more.addEventListener('click', function () {
            var childWrap = document.createElement('div');
            childWrap.className = 'lc-children';
            for (var i = 0; i < children.length; i++) {
              childWrap.appendChild(self.createCommentNode(children[i], depth + 1, comment.id));
            }
            body.replaceChild(childWrap, more);
          });
          body.appendChild(more);
        }
      }
    }

    wrap.appendChild(avatar);
    wrap.appendChild(body);
    return wrap;
  };

  // 移动端扁平回复列表：默认展示前 2 条，其余收进「共 N 条回复」折叠按钮
  dwxComment.prototype.buildFlatReplies = function (flat) {
    var self = this;
    var PREVIEW = 2;
    var wrap = document.createElement('div');
    wrap.className = 'lc-children lc-children-flat';
    var total = flat.length;
    var shown = Math.min(PREVIEW, total);
    for (var i = 0; i < shown; i++) {
      wrap.appendChild(this.createCommentNode(flat[i], 2, flat[i].parentId || 0, true));
    }
    if (total > shown) {
      var more = document.createElement('button');
      more.type = 'button';
      more.className = 'lc-collapse-btn lc-replies-toggle';
      var expanded = false;
      function updateLabel() {
        more.textContent = expanded ? '收起回复' : '共 ' + total + ' 条回复';
      }
      updateLabel();
      more.addEventListener('click', function () {
        expanded = !expanded;
        if (expanded) {
          // 展开：追加剩余回复（标记 lc-expanded，便于收起时精准移除）
          for (var j = shown; j < total; j++) {
            var n = self.createCommentNode(flat[j], 2, flat[j].parentId || 0, true);
            n.classList.add('lc-expanded');
            wrap.insertBefore(n, more);
          }
        } else {
          var nodes = wrap.querySelectorAll('.lc-comment.lc-expanded');
          for (var k = nodes.length - 1; k >= 0; k--) {
            wrap.removeChild(nodes[k]);
          }
        }
        updateLabel();
      });
      wrap.appendChild(more);
    }
    return wrap;
  };

  dwxComment.prototype.like = function (id, btn) {
    var self = this;
    request(this.cfg, {
      method: 'POST',
      url: '/api/v1/comment/' + id + '/like',
      success: function (res) {
        if (res.data) btn.innerHTML = likeLabel(res.data.likeCount);
      },
      error: function (res) {
        self.toast((res && res.msg) || '点赞失败', 'error');
      }
    });
  };

  /* ========== 回复 ========== */

  dwxComment.prototype.startReply = function (comment) {
    this.replyingTo = { id: comment.id, nick: comment.nick, rootId: comment.rootId || comment.id };
    this.replyBarEl.innerHTML = '';
    var span = document.createElement('span');
    span.className = 'lc-reply-info';
    span.textContent = '正在回复 @' + comment.nick;
    var cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'lc-btn lc-reply-cancel';
    cancel.textContent = '取消';
    var self = this;
    cancel.addEventListener('click', function () {
      self.cancelReply();
    });
    this.replyBarEl.appendChild(span);
    this.replyBarEl.appendChild(cancel);
    this.replyBarEl.style.display = 'flex';
    this.textEl.focus();
  };

  dwxComment.prototype.cancelReply = function () {
    this.replyingTo = null;
    this.replyBarEl.innerHTML = '';
    this.replyBarEl.style.display = 'none';
  };

  /* ========== 提交 ========== */

  dwxComment.prototype.submit = function () {
    var self = this;
    var nick = trim(this.nickEl.value);
    var email = trim(this.emailEl.value);
    var link = trim(this.linkEl.value);
    var content = this.textEl.value.replace(/^\s+|\s+$/g, '');

    if (!nick) { this.toast('请填写昵称', 'error'); this.nickEl.focus(); return; }
    if (!content) { this.toast('请填写评论内容', 'error'); this.textEl.focus(); return; }

    var data = {
      pageId: this.pageId,
      site: this.cfg.site,
      nick: nick,
      email: email,
      link: link,
      content: content
    };
    if (this.replyingTo) {
      data.parentId = this.replyingTo.id;
      data.rootId = this.replyingTo.rootId;
    }

    var btn = this.el.querySelector('.lc-submit-btn');
    btn.disabled = true;
    request(this.cfg, {
      method: 'POST',
      url: '/api/v1/comment',
      data: data,
      success: function (res) {
        self.toast('评论已提交', 'success');
        dispatch(self.el, 'lc:submitted', { id: res.data && res.data.id });
        self.textEl.value = '';
        self.charCountEl.textContent = '0/' + self.siteConfig.maxLength;
        self.cancelReply();
        self.saveUser(nick, email, link);
        // 假性通过：本地插入刚提交的评论（背景加深标识待审核），
        // 刷新/重新加载后以服务端真实审核状态为准
        if (res.data && res.data.id) {
          self.insertPendingComment({
            id: res.data.id,
            nick: nick,
            link: link,
            content: content,
            parentId: data.parentId || 0,
            rootId: data.rootId || 0,
            likeCount: 0,
            isPinned: 0,
            isAdmin: 0,
            createTime: Math.floor(Date.now() / 1000)
          });
        }
        // 3 秒防重复
        var t = 3;
        btn.textContent = '已提交 (' + t + 's)';
        var timer = setInterval(function () {
          t--;
          if (t <= 0) {
            clearInterval(timer);
            btn.textContent = '发表评论';
            btn.disabled = false;
          } else {
            btn.textContent = '已提交 (' + t + 's)';
          }
        }, 1000);
      },
      error: function (res) {
        btn.disabled = false;
        self.toast((res && res.msg) || '提交失败，请稍后再试', 'error');
        dispatch(self.el, 'lc:error', { msg: (res && res.msg) || '提交失败' });
      }
    });
  };

  /* ========== 假性通过（待审核）插入 ========== */

  // 提交成功后本地插入评论，标记为待审核（背景加深）。
  // 根评论按当前排序插入；回复则挂到父评论下（桌面挂到父节点子树 / 移动端挂到所属根的扁平回复列表）；
  // 父评论不在当前列表时退化为列表末尾。
  dwxComment.prototype.insertPendingComment = function (comment) {
    var empty = this.listEl.querySelector('.lc-empty');
    if (empty) empty.parentNode.removeChild(empty);

    var node = (this.isMobile && comment.parentId)
      ? this.createCommentNode(comment, 2, comment.parentId, true)
      : this.createCommentNode(comment, 1, comment.parentId || 0);
    node.classList.add('lc-pending');
    node.setAttribute('title', '待审核：审核通过后对所有人可见');

    var appended = false;
    var parent = null;
    if (comment.parentId) {
      parent = this.listEl.querySelector('.lc-comment[data-id="' + comment.parentId + '"]');
    }
    if (parent && this.isMobile) {
      // 移动端：插入到所属根评论的扁平回复列表（折叠时显示在展开按钮前）
      var rootEl = this.listEl.querySelector('.lc-comment[data-id="' + (comment.rootId || comment.parentId) + '"]');
      var flatContainer = rootEl ? rootEl.querySelector('.lc-children-flat') : null;
      if (flatContainer) {
        var toggle = flatContainer.querySelector('.lc-replies-toggle');
        if (toggle) {
          flatContainer.insertBefore(node, toggle);
        } else {
          flatContainer.appendChild(node);
        }
        appended = true;
      }
    } else if (parent) {
      var body = parent.querySelector('.lc-body');
      var children = parent.querySelector('.lc-children');
      if (!children) {
        children = document.createElement('div');
        children.className = 'lc-children';
        body.appendChild(children);
      }
      children.appendChild(node);
      appended = true;
    }
    if (!appended) {
      if (this.sort === 'asc') {
        // “最新”排序（最新在前）：新评论插到列表顶部；有置顶评论时排在置顶之后
        var pinLast = null;
        for (var i = 0; i < this.listEl.children.length; i++) {
          if (this.listEl.children[i].querySelector('.lc-pin')) pinLast = this.listEl.children[i];
        }
        this.listEl.insertBefore(node, pinLast ? pinLast.nextSibling : this.listEl.firstChild);
      } else {
        // 倒序/热度：新评论追加到列表末尾
        this.listEl.appendChild(node);
      }
    }

    // 本地计数 +1，保持与假性展示一致（刷新后以服务端真实数据为准）
    this.total += 1;
    if (this.countEl) this.countEl.textContent = '(' + this.total + ')';
  };

  /* ========== 用户记忆 ========== */

  dwxComment.prototype.saveUser = function (nick, email, link) {
    try {
      localStorage.setItem('lc_user', JSON.stringify({ nick: nick, email: email, link: link }));
    } catch (e) { /* ignore */ }
  };

  dwxComment.prototype.restoreUser = function () {
    var self = this;
    var data = null;
    try { data = JSON.parse(localStorage.getItem('lc_user') || 'null'); } catch (e) { /* ignore */ }
    if (data && data.nick) {
      this.nickEl.value = data.nick || '';
      this.emailEl.value = data.email || '';
      this.linkEl.value = data.link || '';
      this.clearBtnEl.style.display = 'inline-block';
    }
  };

  /* ========== Toast ========== */

  dwxComment.prototype.toast = function (msg, type) {
    var self = this;
    this.toastEl.textContent = msg;
    this.toastEl.className = 'lc-toast lc-toast-' + (type || 'info');
    this.toastEl.style.display = 'block';
    if (this._toastTimer) clearTimeout(this._toastTimer);
    this._toastTimer = setTimeout(function () {
      self.toastEl.style.display = 'none';
    }, 3000);
  };

  /* ========== 初始化 ========== */

  function init() {
    var els = document.querySelectorAll('[data-page-id]');
    for (var i = 0; i < els.length; i++) {
      if (!els[i]._dwxComment) {
        els[i]._dwxComment = new dwxComment(els[i]);
      }
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  // 构造器单独导出：window.dwxComment 保留为配置入口（不会被覆盖）
  window.DwxComment = dwxComment;
})(window, document);
