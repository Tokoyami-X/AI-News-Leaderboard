const $ = id => document.getElementById(id);
const PAGE_SIZE = 9;
const state = { source: 'all', query: '', items: [], busy: false, lang: localStorage.getItem('ai-radar-lang') || 'zh', translations: new Map(), translating: false, switchingLanguage: false, page: 1 };
const colors = { openai: '#b5f36b', google: '#8eb7ff', anthropic: '#f2a77d', huggingface: '#f6d66b', chinaai: '#ff7777', chinamodels: '#ffad66', chinastartups: '#d49aff', chinapolicy: '#6bd9c3', chinaembodied: '#f0d46b' };
const sourceLabels = { openai:{zh:'OpenAI',en:'OpenAI'}, chinaai:{zh:'中国 AI 综合',en:'China AI'}, chinamodels:{zh:'大模型/产品',en:'Models & Products'}, chinastartups:{zh:'中国 AI 公司',en:'AI Companies'}, chinapolicy:{zh:'政策/产业',en:'Policy & Industry'}, chinaembodied:{zh:'机器人/具身智能',en:'Robotics'}, google:{zh:'Google AI',en:'Google AI'}, anthropic:{zh:'Anthropic',en:'Anthropic'}, huggingface:{zh:'Hugging Face',en:'Hugging Face'} };
const words = {
  zh: { refresh:'↻ 刷新新闻', refreshing:'正在刷新…', search:'搜索模型、公司、研究关键词…', all:'全部', stats:'篇新闻', sources:'个新闻源', official:'官方动态聚合', latest:'最新动态', wait:'等待数据', noMatch:'暂时没有匹配的新闻', tryAgain:'更换关键词或来源，或稍后刷新重试。', loading:'正在连接官方新闻源…', read:'阅读原文 ↗', lang:'EN', updating:'正在翻译当前页新闻…', translated:'当前页中文翻译完成', translateError:'部分翻译失败，失败内容会显示英文原文。', translationUnavailable:'翻译服务暂时不可用，显示原文。', note:'公开 RSS / Atom 聚合 · 发布时间以原始来源为准', heroTitle:'看见 AI 的', heroAccent:'下一步。', heroDesc:'从前沿模型到研究突破，把值得关注的人工智能动态汇聚于此。', newsFeed:'全球 AI 动态', prev:'← 上一页', next:'下一页 →', page:'第', of:'/', pages:'页', perPage:'每页 9 条新闻', pageInvalid:'请输入有效页码。' },
  en: { refresh:'↻ Refresh news', refreshing:'Refreshing…', search:'Search models, companies, research…', all:'All sources', stats:'articles', sources:'news sources', official:'Official updates', latest:'Latest updates', wait:'Loading', noMatch:'No matching news yet', tryAgain:'Try another keyword or source, or refresh later.', loading:'Connecting to official news feeds…', read:'Read original ↗', lang:'中文', updating:'Translating this page…', translated:'Current page translated', translateError:'Some translations failed; failed items remain in English.', translationUnavailable:'Translation unavailable; showing original text.', note:'Public RSS / Atom aggregation · Times are from original sources', heroTitle:'See where AI goes', heroAccent:'next.', heroDesc:'Frontier models, research breakthroughs, and the AI developments worth following.', newsFeed:'GLOBAL AI SIGNALS', prev:'← Previous', next:'Next →', page:'Page', of:'/', pages:'pages', perPage:'9 articles per page', pageInvalid:'Enter a valid page number.' }
};
function w(k) { return words[state.lang][k] || k; }
function formatDate(value) { if (!value || value.startsWith('0001-')) return state.lang === 'zh' ? '日期未知' : 'Date unknown'; const d = new Date(value); if (Number.isNaN(d.getTime())) return state.lang === 'zh' ? '日期未知' : 'Date unknown'; return new Intl.DateTimeFormat(state.lang === 'zh' ? 'zh-CN' : 'en-US', { month:'short', day:'numeric', hour:'2-digit', minute:'2-digit', hour12:false }).format(d); }
function esc(s) { return String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function safeURL(v) { try { const u = new URL(v); return ['https:','http:'].includes(u.protocol) ? u.href : '#'; } catch { return '#'; } }
function displayed(it, field) { return state.lang === 'en' ? (it[field] || '') : (state.translations.get(it.id + '|' + field) || it[field] || ''); }
function getFiltered() { const q = state.query.toLowerCase(); return state.items.filter(it => (state.source === 'all' || state.source === it.sourceId) && (!q || (it.title + ' ' + it.summary + ' ' + (state.translations.get(it.id + '|title') || '') + ' ' + (state.translations.get(it.id + '|summary') || '') + ' ' + it.source).toLowerCase().includes(q))); }
function render(items = getFiltered()) {
  $('totalCount').textContent = state.items.length;
  $('resultsLabel').textContent = items.length + (state.lang === 'zh' ? ' 条结果' : ' results');
  const pages = Math.max(1, Math.ceil(items.length / PAGE_SIZE));
  state.page = Math.min(Math.max(1, state.page), pages);
  const pageItems = items.slice((state.page - 1) * PAGE_SIZE, state.page * PAGE_SIZE);
  const grid = $('newsGrid');
  if (!pageItems.length) {
    grid.innerHTML = '<div class="empty-state"><strong>' + esc(w('noMatch')) + '</strong><span>' + esc(w('tryAgain')) + '</span></div>';
  } else {
    grid.innerHTML = pageItems.map((it, i) => {
      const link = esc(safeURL(it.url));
      const globalIndex = (state.page - 1) * PAGE_SIZE + i + 1;
      return '<article class="news-card" data-news-id="' + esc(it.id) + '" style="animation-delay:' + (i * 35) + 'ms"><div class="card-top"><span class="source-tag" style="--tag:' + (colors[it.sourceId] || '#b5f36b') + '">' + esc(it.source) + '</span><time class="card-date">' + esc(formatDate(it.publishedAt)) + '</time></div><h3><a href="' + link + '" target="_blank" rel="noopener noreferrer">' + esc(displayed(it, 'title')) + '</a></h3><p class="card-summary">' + esc(displayed(it, 'summary') || (state.lang === 'zh' ? '原始新闻源未提供摘要，点击标题查看完整内容。' : 'No summary supplied by source. Open the article for details.')) + '</p><div class="card-bottom"><a class="read-link" href="' + link + '" target="_blank" rel="noopener noreferrer">' + esc(w('read')) + '</a><span class="card-index">NO. ' + String(globalIndex).padStart(2, '0') + '</span></div></article>';
    }).join('');
  }
  $('prevPage').disabled = state.page <= 1;
  $('nextPage').disabled = state.page >= pages;
  $('pageInput').value = state.page;
  $('pageInput').max = pages;
  $('pageTotal').textContent = pages;
  $('pageInfo').textContent = w('perPage');
}
function goPage(value) {
  const items = getFiltered();
  const pages = Math.max(1, Math.ceil(items.length / PAGE_SIZE));
  const target = Number(value);
  if (!Number.isInteger(target) || target < 1 || target > pages) {
    $('pageInput').value = state.page;
    $('pageInput').setCustomValidity(w('pageInvalid'));
    $('pageInput').reportValidity();
    return;
  }
  $('pageInput').setCustomValidity('');
  state.page = target;
  render(items);
  if (state.lang === 'zh') translateCurrent(items);
  $('newsGrid').scrollIntoView({ behavior:'smooth', block:'start' });
}
async function translateCurrent(items = getFiltered()) {
  if (state.lang !== 'zh' || state.translating) return;
  const pageItems = items.slice((state.page - 1) * PAGE_SIZE, state.page * PAGE_SIZE);
  const pending = pageItems.filter(it => !state.translations.has(it.id + '|title') || !state.translations.has(it.id + '|summary'));
  if (!pending.length) return;
  const requestedPage = state.page;
  state.translating = true;
  $('updateNote').textContent = w('updating');
  let failed = false;
  try {
    // Nine cards means at most 18 strings per request, below the API's 30-string limit.
    const texts = pending.flatMap(it => [it.title || '', it.summary || '']);
    const res = await fetch('/api/translate', { method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({texts}), cache:'no-store' });
    if (!res.ok) throw Error('HTTP ' + res.status);
    const data = await res.json();
    for (let i = 0; i < pending.length; i++) {
      const title = data.translations?.[i * 2] || pending[i].title || '';
      const summary = data.translations?.[i * 2 + 1] || pending[i].summary || '';
      // Cache even empty summaries and failed-item fallbacks to avoid repeated requests.
      state.translations.set(pending[i].id + '|title', title);
      state.translations.set(pending[i].id + '|summary', summary);
    }
    failed = Number(data.failed || 0) > 0;
  } catch (e) { failed = true; }
  finally {
    state.translating = false;
    render(getFiltered());
    $('updateNote').textContent = failed ? w('translateError') : w('translated');
    // Only queue another request when the user navigated to a different page mid-request.
    if (state.lang === 'zh' && state.page !== requestedPage) translateCurrent(getFiltered());
  }
}
async function loadNews(force = false) {
  if (state.busy) return;
  state.busy = true;
  $('refreshBtn').disabled = true;
  $('refreshBtn').textContent = w('refreshing');
  $('statusMessage').hidden = true;
  if (!state.items.length) $('newsGrid').innerHTML = '<div class="loading-card">' + esc(w('loading')) + '</div>';
  try {
    const p = new URLSearchParams();
    if (state.source !== 'all') p.set('source', state.source);
    if (force) p.set('refresh', '1');
    const res = await fetch('/api/news?' + p, {cache:'no-store'});
    if (!res.ok) throw Error('HTTP ' + res.status);
    const data = await res.json();
    state.items = data.items || [];
    state.page = 1;
    render(getFiltered());
    $('sourceCount').textContent = (data.sources || []).length || 4;
    $('freshness').textContent = data.fetchedAt ? (data.stale ? 'STALE' : 'LIVE') : 'WAIT';
    $('updateNote').textContent = data.fetchedAt ? (state.lang === 'zh' ? '上次同步 ' : 'Last synced ') + formatDate(data.fetchedAt) : w('wait');
    if (data.errors?.length) {
      $('statusMessage').textContent = (state.lang === 'zh' ? '部分新闻源暂时不可用：' : 'Some news feeds are temporarily unavailable: ') + data.errors.join('; ');
      $('statusMessage').hidden = false;
    } else if (!data.items?.length) {
      const waiting = !data.fetchedAt;
      $('statusMessage').textContent = waiting
        ? (state.lang === 'zh' ? '正在同步新闻源，首次加载可能需要十几秒…' : 'Syncing news feeds; first load may take several seconds…')
        : (state.lang === 'zh' ? '当前分类暂时没有新闻，请稍后刷新。' : 'No articles in this category right now. Try refreshing shortly.');
      $('statusMessage').hidden = false;
    }
    if (state.lang === 'zh') translateCurrent(getFiltered());
  } catch (e) {
    $('statusMessage').textContent = (state.lang === 'zh' ? '暂时无法连接新闻服务：' : 'Cannot connect to news service: ') + e.message;
    $('statusMessage').hidden = false;
    if (!state.items.length) $('newsGrid').innerHTML = '<div class="empty-state"><strong>' + esc(state.lang === 'zh' ? '新闻源暂时不可达' : 'News feeds temporarily unavailable') + '</strong></div>';
  } finally {
    state.busy = false;
    $('refreshBtn').disabled = false;
    $('refreshBtn').textContent = w('refresh');
  }
}
function applyLanguage(skipRender = false) {
  document.documentElement.lang = state.lang === 'zh' ? 'zh-CN' : 'en';
  $('langToggle').textContent = w('lang');
  $('refreshBtn').textContent = w('refresh');
  $('searchInput').placeholder = w('search');
  $('allTab').textContent = w('all');
  document.querySelectorAll('.source-tab[data-source]').forEach(tab => { const labels = sourceLabels[tab.dataset.source]; if (labels) tab.textContent = labels[state.lang]; });
  $('statsNewsLabel').textContent = w('stats');
  $('statsSourceLabel').textContent = w('sources');
  $('statsOfficialLabel').textContent = w('official');
  $('newsTitle').textContent = w('latest');
  $('newsFeedLabel').textContent = state.lang === 'zh' ? '/ NEWS FEED' : '/ 最新动态';
  $('heroLead').textContent = w('heroTitle');
  $('heroAccent').textContent = w('heroAccent');
  $('heroDesc').textContent = w('heroDesc');
  $('bottomNoteText').textContent = w('note');
  $('topMetaLabel').textContent = w('newsFeed');
  $('prevPage').textContent = w('prev');
  $('nextPage').textContent = w('next');
  $('pageLabelPrefix').textContent = state.lang === 'zh' ? '第' : 'Page';
  $('pageLabelSuffix').textContent = state.lang === 'zh' ? '页' : 'pages';
  $('pageInput').setAttribute('aria-label', state.lang === 'zh' ? '页码' : 'Page number');
  if (!skipRender) {
    render(getFiltered());
    if (state.lang === 'zh') translateCurrent(getFiltered());
    else $('updateNote').textContent = state.items.length ? w('translated') : w('wait');
  }
}
async function switchLanguage() {
  if (state.switchingLanguage) return;
  const nextLang = state.lang === 'zh' ? 'en' : 'zh';
  state.switchingLanguage = true;
  $('langToggle').disabled = true;
  state.lang = nextLang;
  localStorage.setItem('ai-radar-lang', state.lang);
  applyLanguage(true);

  if (state.lang === 'zh') {
    $('updateNote').textContent = w('updating');
    await translateCurrent(getFiltered());
  }

  render(getFiltered());
  $('langToggle').disabled = false;
  state.switchingLanguage = false;
}
$('langToggle').addEventListener('click', switchLanguage);
$('refreshBtn').addEventListener('click', () => loadNews(true));
$('sourceTabs').addEventListener('click', e => {
  const b = e.target.closest('[data-source]');
  if (!b) return;
  state.source = b.dataset.source;
  state.page = 1;
  document.querySelectorAll('.source-tab').forEach(t => t.classList.toggle('active', t === b));
  loadNews();
});
let timer;
$('searchInput').addEventListener('input', e => {
  state.query = e.target.value.trim();
  state.page = 1;
  clearTimeout(timer);
  timer = setTimeout(() => { render(getFiltered()); if (state.lang === 'zh') translateCurrent(getFiltered()); }, 200);
});
$('prevPage').addEventListener('click', () => goPage(state.page - 1));
$('nextPage').addEventListener('click', () => goPage(state.page + 1));
$('pageInput').addEventListener('change', e => goPage(e.target.value));
$('pageInput').addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); goPage(e.target.value); } });
$('pageInput').addEventListener('input', e => e.target.setCustomValidity(''));
$('clock').textContent = new Intl.DateTimeFormat(state.lang === 'zh' ? 'zh-CN' : 'en-US', {year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date());
applyLanguage();
loadNews();
setInterval(() => { if (!state.items.length && !state.busy) loadNews(); }, 3000);