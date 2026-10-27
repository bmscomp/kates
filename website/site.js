// Mobile navigation toggle and footer year. No dependencies.
(function () {
  var toggle = document.querySelector('.nav-toggle');
  var links = document.getElementById('nav-links');
  if (toggle && links) {
    toggle.addEventListener('click', function () {
      var open = links.classList.toggle('open');
      toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
      toggle.setAttribute('aria-label', open ? 'Close menu' : 'Open menu');
    });
    links.addEventListener('click', function (e) {
      if (e.target.tagName === 'A') {
        links.classList.remove('open');
        toggle.setAttribute('aria-expanded', 'false');
      }
    });
  }
  var year = document.getElementById('year');
  if (year) year.textContent = String(new Date().getFullYear());
})();

// Architecture diagram: entrance once in view, pause off-screen, highlight a box and its links on hover or focus.
(function () {
  var arch = document.querySelector('.arch');
  if (!arch || !arch.querySelector('.node')) return;
  var reduce = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  if (!reduce && 'IntersectionObserver' in window) {
    arch.classList.add('anim');
    new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) arch.classList.add('seen');
        arch.classList.toggle('paused', !e.isIntersecting);
      });
    }, { threshold: 0.2 }).observe(arch);
  }
  var cap = arch.querySelector('figcaption');
  var base = cap.textContent;
  function show(node) {
    var id = node.getAttribute('data-node');
    var svg = node.ownerSVGElement;
    clear();
    arch.classList.add('focus');
    node.classList.add('on');
    svg.querySelectorAll('.link').forEach(function (l) {
      var a = l.getAttribute('data-a'), b = l.getAttribute('data-b');
      if (a !== id && b !== id) return;
      l.classList.add('on');
      svg.querySelector('[data-node="' + (a === id ? b : a) + '"]').classList.add('on');
    });
    cap.textContent = arch.querySelector('[data-node="' + id + '"][data-note]').getAttribute('data-note');
  }
  function clear() {
    arch.classList.remove('focus');
    arch.querySelectorAll('.on').forEach(function (el) { el.classList.remove('on'); });
    cap.textContent = base;
  }
  arch.querySelectorAll('.node').forEach(function (node) {
    node.addEventListener('mouseenter', function () { show(node); });
    node.addEventListener('focus', function () { show(node); });
    node.addEventListener('mouseleave', clear);
    node.addEventListener('blur', clear);
  });

  // The disruption story: five phases on a 13 s loop, or a click on the timeline. Time only runs while the figure is on screen.
  var D = 13000, N = 5;
  var steps = ['Steady load: measuring the baseline', 'Injecting broker-pod-kill through LitmusChaos',
    'Broker down: ISR shrinks, P99 climbs', 'Broker back: recovery time measured', 'Run graded A and stored; the CLI exits 0'];
  var btns = arch.querySelectorAll('.arch-tl button'), bar = arch.querySelector('.arch-tl-bar span'), step = arch.querySelector('.arch-step');
  var kzFig = document.querySelector('.kz');
  var designBtns = document.querySelectorAll('[data-set-design]');
  var winTitle = document.getElementById('arch-wintitle'), kzWinTitle = document.getElementById('kz-wintitle');
  var designOrder = ['grid', 'panels', 'chips'];
  var designTitles = {
    grid: 'kates architecture — grid look, C → aurora',
    panels: 'kates architecture — T → panels, C → dracula',
    chips: 'kates architecture — classic look, C → chips / aurora'
  };
  var kzDesignTitles = {
    grid: 'kates kafka tui — grid look, C → aurora',
    panels: 'kates kafka tui — T → panels, C → dracula',
    chips: 'kates kafka tui — classic look, C → chips / aurora'
  };
  function setDesign(name) {
    arch.setAttribute('data-tui-design', name);
    if (kzFig) kzFig.setAttribute('data-tui-design', name);
    if (winTitle && designTitles[name]) winTitle.textContent = designTitles[name];
    if (kzWinTitle && kzDesignTitles[name]) kzWinTitle.textContent = kzDesignTitles[name];
    designBtns.forEach(function (b) {
      b.setAttribute('aria-pressed', b.getAttribute('data-set-design') === name ? 'true' : 'false');
    });
  }
  designBtns.forEach(function (b) {
    b.addEventListener('click', function () { setDesign(b.getAttribute('data-set-design')); });
  });
  document.addEventListener('keydown', function (e) {
    if (arch.classList.contains('paused') && (!kzFig || kzFig.classList.contains('paused'))) return;
    if (e.target && (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA')) return;
    if (e.key === 'T' || e.key === 'C') {
      var cur = arch.getAttribute('data-tui-design') || 'grid';
      var idx = (designOrder.indexOf(cur) + 1) % designOrder.length;
      setDesign(designOrder[idx]);
    }
  });
  var clip = arch.querySelector('#p99-clip rect'), head = arch.querySelector('.p99-head'), val = arch.querySelector('.p99');
  var X0 = 784, W = 132;
  function ms(p) { // illustrative P99 in ms, matching the hero terminal: 17.8 baseline, 41.2 with a broker down
    var v = 17.8 + 0.9 * Math.sin(p * 97) + 0.6 * Math.sin(p * 41);
    if (p >= 0.4 && p < 0.6) v += 23.4 * Math.min(1, (p - 0.4) * 15);
    else if (p >= 0.6 && p < 0.8) v += 23.4 * Math.max(0, 1 - (p - 0.6) * 6);
    return v;
  }
  function y(v) { return 330 - (v - 10) * 3; }
  var d = '';
  for (var i = 0; i <= 200; i++) d += (i ? 'L' : 'M') + (X0 + W * i / 200).toFixed(1) + ' ' + y(ms(i / 200)).toFixed(1);
  arch.querySelector('.p99-line').setAttribute('d', d);
  arch.querySelector('.p99-area').setAttribute('d', d + 'L' + (X0 + W) + ' 340L' + X0 + ' 340Z');
  var t = 0, last = 0;
  function render(p) {
    var ph = Math.min(N - 1, Math.floor(p * N));
    if (arch.getAttribute('data-phase') !== String(ph)) {
      arch.setAttribute('data-phase', ph);
      step.textContent = steps[ph];
      btns.forEach(function (b, k) { b.setAttribute('aria-pressed', k === ph ? 'true' : 'false'); });
    }
    clip.setAttribute('width', W * p);
    head.setAttribute('cx', X0 + W * p);
    head.setAttribute('cy', y(ms(p)));
    val.textContent = ms(p).toFixed(1) + ' ms';
    bar.style.width = p * 100 + '%';
  }
  btns.forEach(function (b, k) {
    b.addEventListener('click', function () { t = k / N * D; if (reduce) render((k + 0.99) / N); });
  });
  if (reduce) return render(0.99);
  requestAnimationFrame(function tick(now) {
    if (last && !arch.classList.contains('paused')) t = (t + now - last) % D;
    last = now;
    render(t / D);
    requestAnimationFrame(tick);
  });
})();

// Inside Kafka: the same broker kill seen from the brokers, one phase every 2.8 s while on screen.
(function () {
  var kz = document.querySelector('.kz');
  if (!kz) return;
  var cap = kz.querySelector('figcaption'), lanes = kz.querySelectorAll('.kz-lane');
  var move = kz.querySelector('[data-move]'), take = kz.querySelector('[data-take]');
  var steps = [
    '<b>Steady load.</b> Every partition has 3 in-sync replicas; each broker leads one.',
    '<b>Inject.</b> LitmusChaos runs broker-pod-kill on krafter-kafka-1.',
    '<b>Broker down.</b> p1 leadership moves to zone c, ISR drops to 2, P99 17.8 → 41.2 ms.',
    '<b>Recovery.</b> krafter-kafka-1 is back and catching up on what it missed.',
    '<b>Healthy.</b> ISR back to 3, preferred leader restored. Recovered in 6.4 s, 0 records lost.'
  ];
  function set(ph) {
    var down = ph === 2 || ph === 3;
    kz.setAttribute('data-phase', ph);
    cap.innerHTML = steps[ph];
    move.classList.toggle('ldr', !down);
    take.classList.toggle('ldr', down);
    lanes[1].classList.toggle('idle', down);
    lanes[2].classList.toggle('busy', down);
  }
  if (window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches) return set(2);
  var ph = 0, on = true;
  if ('IntersectionObserver' in window) {
    new IntersectionObserver(function (entries) {
      on = entries[0].isIntersecting;
      kz.classList.toggle('paused', !on);
    }, { threshold: 0.2 }).observe(kz);
  }
  set(0);
  setInterval(function () { if (on) set(ph = (ph + 1) % steps.length); }, 2800);
})();
