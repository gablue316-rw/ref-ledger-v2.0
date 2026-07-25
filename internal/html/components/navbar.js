//
// Ref Ledger shared navbar
//

document.addEventListener("DOMContentLoaded", async function () {

    const container =
        document.getElementById("navbar-container");

    if (!container) {
        return;
    }

    try {

        const response =
            await fetch("/components/navbar.html");

        if (!response.ok) {
            throw new Error(
                `Unable to load navbar: HTTP ${response.status}`
            );
        }

        container.innerHTML =
            await response.text();

        highlightCurrentPage();

        initializeLogout();

        await loadCurrentUser();
        await loadPendingGames();

    } catch (error) {

        console.error(
            "Unable to initialize navbar:",
            error
        );

    }

});

//
// Highlight the current page
//

function highlightCurrentPage() {

    let page =
        window.location.pathname;

    if (page === "/") {
        page = "home";
    } else {
        page = page
            .replace(/^\/+/, "")
            .replace(/\/+$/, "");
    }

    const activeLink =
        document.querySelector(
            `.nav-links a[data-page="${page}"]`
        );

    if (activeLink) {
        activeLink.classList.add("active");
    }

}

//
// Load logged-in user
//

async function loadCurrentUser() {

    const userName =
        document.getElementById("userName");

    const userRole =
        document.getElementById("userRole");

    if (!userName || !userRole) {
        return;
    }

    try {

        const response =
            await fetch("/api/session");

        if (response.status === 401) {
            window.location.href = "/login";
            return;
        }

        if (!response.ok) {
            throw new Error(
                `Unable to load session: HTTP ${response.status}`
            );
        }

        const session =
            await response.json();

        userName.textContent =
            session.name ||
            session.username ||
            session.email ||
            "Unknown User";

        userRole.textContent =
            formatRole(session.role);

    } catch (error) {

        console.error(
            "Unable to load user information:",
            error
        );

        userName.textContent =
            "Unknown User";

        userRole.textContent =
            "";

    }

}

//
// Load  pending game counts
//

async function loadPendingGames() {

    const badge =
        document.getElementById("pendingGames");

    if (!badge) {
        return;
    }

    try {

        const response =
            await fetch("/api/games/pending-games/count");

        if (!response.ok) {
            throw new Error(
                `Unable to load pending game count: HTTP ${response.status}`
            );
        }

        const result =
            await response.json();

        const oneDayCount =
            result.oneDayCount ?? 0;

        const sevenDayCount = 
            result.sevenDayCount ?? 0;

        badge.textContent =
            `${oneDayCount}/${sevenDayCount}`;
        
        badge.title =
            `${oneDayCount} pending game${oneDayCount === 1 ? "" : "s"} today / ` +
            `${sevenDayCount} total pending game${sevenDayCount === 1 ? "" : "s"} through the next 7 days`;

        badge.setAttribute(
            "aria-label",
            badge.title
        );

    } catch (error) {

        console.error(
            "Unable to load pending games count:",
            error
        );

        badge.textContent =
            "—";

        badge.title =
            "Unable to load pending games";

        badge.setAttribute(
            "aria-label",
            badge.title
        );
    
    }

}

//
// Format roles such as site_admin
//

function formatRole(role) {

    if (!role) {
        return "";
    }

    return role
        .replaceAll("_", " ")
        .replaceAll("-", " ")
        .replace(
            /\b\w/g,
            character => character.toUpperCase()
        );

}

//
// Logout
//

function initializeLogout() {

    const logoutButton =
        document.getElementById("logoutBtn");

    if (!logoutButton) {
        return;
    }

    logoutButton.addEventListener(
        "click",
        logoutUser
    );

}

async function logoutUser(event) {

    event.preventDefault();

    try {

        const response =
            await fetch("/api/logout", {
                method: "POST"
            });

        if (!response.ok) {
            throw new Error(
                `Logout failed: HTTP ${response.status}`
            );
        }

        window.location.href =
            "/login";

    } catch (error) {

        console.error(
            "Unable to log out:",
            error
        );

        alert("Logout failed.");

    }

}