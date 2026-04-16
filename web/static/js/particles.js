/* Particle network background - port of simplego.dev/assets
   Dark/light aware, mouse-reactive, pauses when tab hidden. */

(function () {
  var c = document.getElementById('sg-bg-canvas');
  if (!c) return;
  var ctx = c.getContext('2d');
  if (!ctx) return;

  var particles = [];
  var mouse = { x: -9999, y: -9999 };
  var W, H, dpr, raf;
  var CONNECT_DIST = 140;
  var MOUSE_DIST = 180;
  var count = 0;

  function isDark() { return document.documentElement.getAttribute('data-theme') === 'dark'; }

  function colors() {
    if (isDark()) {
      return { node: 'rgba(69,189,209,', line: 'rgba(69,189,209,', glow: 'rgba(69,189,209,',
               nodeAlpha: 0.5, lineAlpha: 0.12, glowAlpha: 0.08 };
    }
    return { node: 'rgba(26,125,90,', line: 'rgba(26,125,90,', glow: 'rgba(26,125,90,',
             nodeAlpha: 0.35, lineAlpha: 0.08, glowAlpha: 0.05 };
  }

  function resize() {
    dpr = Math.min(window.devicePixelRatio || 1, 2);
    W = window.innerWidth; H = window.innerHeight;
    c.width = W * dpr; c.height = H * dpr;
    c.style.width = W + 'px'; c.style.height = H + 'px';
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    var target = Math.floor((W * H) / 14000);
    target = Math.max(30, Math.min(target, 120));
    if (W < 768) target = Math.min(target, 35);
    while (particles.length < target) particles.push(make());
    while (particles.length > target) particles.pop();
    count = particles.length;
  }

  function make() {
    var speed = 0.15 + Math.random() * 0.35;
    var angle = Math.random() * Math.PI * 2;
    return {
      x: Math.random() * W, y: Math.random() * H,
      vx: Math.cos(angle) * speed, vy: Math.sin(angle) * speed,
      r: 1 + Math.random() * 1.8,
      pulse: Math.random() * Math.PI * 2,
      pulseSpeed: 0.005 + Math.random() * 0.015,
      bright: Math.random() > 0.85
    };
  }

  function draw() {
    ctx.clearRect(0, 0, W, H);
    var col = colors();
    for (var i = 0; i < count; i++) {
      var p = particles[i];
      p.pulse += p.pulseSpeed;
      p.x += p.vx; p.y += p.vy;
      if (p.x < -10) p.x = W + 10; if (p.x > W + 10) p.x = -10;
      if (p.y < -10) p.y = H + 10; if (p.y > H + 10) p.y = -10;
      var mx = mouse.x - p.x, my = mouse.y - p.y;
      var md = Math.sqrt(mx * mx + my * my);
      if (md < MOUSE_DIST && md > 1) {
        var force = (1 - md / MOUSE_DIST) * 0.4;
        p.vx -= (mx / md) * force * 0.08;
        p.vy -= (my / md) * force * 0.08;
      }
      p.vx *= 0.999; p.vy *= 0.999;
      var alpha = col.nodeAlpha * (0.6 + 0.4 * Math.sin(p.pulse));
      if (p.bright) alpha = Math.min(alpha * 1.8, 0.9);
      ctx.beginPath(); ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2);
      ctx.fillStyle = col.node + alpha + ')'; ctx.fill();
      if (p.bright) {
        ctx.beginPath(); ctx.arc(p.x, p.y, p.r * 4, 0, Math.PI * 2);
        ctx.fillStyle = col.glow + col.glowAlpha * Math.sin(p.pulse) * 0.5 + ')'; ctx.fill();
      }
    }
    for (var i2 = 0; i2 < count; i2++) {
      for (var j = i2 + 1; j < count; j++) {
        var a = particles[i2], b = particles[j];
        var dx = a.x - b.x, dy = a.y - b.y;
        var dist = Math.sqrt(dx * dx + dy * dy);
        if (dist < CONNECT_DIST) {
          var la = col.lineAlpha * (1 - dist / CONNECT_DIST);
          var midX = (a.x + b.x) * 0.5, midY = (a.y + b.y) * 0.5;
          var mDist = Math.sqrt((mouse.x - midX) * (mouse.x - midX) + (mouse.y - midY) * (mouse.y - midY));
          if (mDist < MOUSE_DIST) la *= 1 + 1.5 * (1 - mDist / MOUSE_DIST);
          ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y);
          ctx.strokeStyle = col.line + la + ')'; ctx.lineWidth = 0.6; ctx.stroke();
        }
      }
    }
    raf = requestAnimationFrame(draw);
  }

  document.addEventListener('mousemove', function (e) { mouse.x = e.clientX; mouse.y = e.clientY; });
  document.addEventListener('mouseleave', function () { mouse.x = -9999; mouse.y = -9999; });

  resize();
  var resizeTimer;
  window.addEventListener('resize', function () {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(resize, 150);
  });
  draw();

  document.addEventListener('visibilitychange', function () {
    if (document.hidden) cancelAnimationFrame(raf);
    else raf = requestAnimationFrame(draw);
  });
})();
