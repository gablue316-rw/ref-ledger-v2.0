(() => {
    function updateAdministrationLink() {
        const link = document.getElementById('administrationNavLink');
        if (!link) return;
        const isAdmin = (document.getElementById('userRole')?.textContent || '')
            .trim().toLowerCase() === 'admin';
        link.hidden = !isAdmin;
        link.style.display = isAdmin ? '' : 'none';
        if (isAdmin && window.location.pathname === '/administration') {
            link.classList.add('active');
        }
    }
    const container = document.getElementById('navbar-container');
    if (container) {
        new MutationObserver(updateAdministrationLink).observe(container, {
            childList: true, subtree: true, characterData: true
        });
    }
    updateAdministrationLink();
})();
