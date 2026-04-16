/* Theme toggle - mirrors simplego.dev
   - Default: dark
   - Storage key: sg-theme
   - Pills with data-set-theme="light|dark"
*/

(function () {
  var meta = document.getElementById('meta-theme-color');
  var light = document.getElementById('tp-light');
  var dark = document.getElementById('tp-dark');
  var pills = document.getElementById('theme-pills');

  function apply(name) {
    document.documentElement.setAttribute('data-theme', name);
    try { localStorage.setItem('sg-theme', name); } catch (e) {}
    if (meta) meta.content = name === 'dark' ? '#050A12' : '#FAFBFD';
    if (light) light.classList.toggle('active', name === 'light');
    if (dark) dark.classList.toggle('active', name === 'dark');
  }

  var current = document.documentElement.getAttribute('data-theme') || 'dark';
  if (light) light.classList.toggle('active', current === 'light');
  if (dark) dark.classList.toggle('active', current === 'dark');

  if (pills) {
    pills.addEventListener('click', function (e) {
      var a = e.target.closest('[data-set-theme]');
      if (!a) return;
      e.preventDefault();
      apply(a.dataset.setTheme);
    });
  }
})();
