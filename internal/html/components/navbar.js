//
// Ref Ledger shared navbar
//

document.addEventListener("DOMContentLoaded", initializeNavbar);

//
// Load and initialize the shared navbar
//

async function initializeNavbar() {

    const container =
        document.getElementById("navbar-container");

    if (!container) {
        return;
    }

    try {

        const response =
            await fetch("/components/navbar.html", {
                cache: "no-cache"
            });

        if (!response.ok) {
            throw new Error(
                `Unable to load navbar: HTTP ${response.status}`
            );
        }

        container.innerHTML =
            await response.text();

        highlightCurrentPage();
        initializeLogout();

        await Promise.all([
            loadCurrentUser(),
            loadPendingGames(),
            loadEnvironment()
        ]);

    } catch (error) {

        console.error(
            "Unable to initialize navbar:",
            error
        );

    }

}

//
// Highlight the current navigation link
//

function highlightCurrentPage() {

    const pathname =
        window.location.pathname;

    let page;

    if (pathname === "/") {

        page = "home";

    } else {

        page = pathname
            .replace(/^\/+/, "")
            .replace(/\/+$/, "");

    }

    const navigationLinks =
        document.querySelectorAll(
            ".nav-links a[data-page]"
        );

    navigationLinks.forEach(function (link) {

        const linkPage =
            link.getAttribute("data-page");

        link.classList.toggle(
            "active",
            linkPage === page
        );

    });

}

//
// Load the logged-in user
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
            await fetch("/api/session", {
                cache: "no-cache"
            });

        if (response.status === 401) {

            window.location.href =
                "/login";

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
// Load environment information
//
async function loadEnvironment() {
    const navbar =
        document.getElementById("main-navbar");

    const environmentLabel =
        document.getElementById("environment-label");

    if (!navbar || !environmentLabel) {
        return;
    }

    try {
        const response =
            await fetch("/api/environment", {
                cache: "no-cache"
            });

        if (!response.ok) {
            throw new Error(
                `Unable to load environment: HTTP ${response.status}`
            );
        }

        const environmentInfo =
            await response.json();

        const environment =
            String(environmentInfo.environment || "")
                .toLowerCase();

        if (environment === "development") {
            navbar.classList.add(
                "navbar-development"
            );
        } else {
            navbar.classList.remove(
                "navbar-development"
            );
        }

        environmentLabel.replaceChildren();

        [
           environmentInfo.environment || "Unknown",
           environmentInfo.hostLabel || "Unknown Host",
           environmentInfo.database || "Unknown Database"
        ].forEach(value => {
            const line = document.createElement("div");
            line.textContent = value;
            environmentLabel.appendChild(line);
        });

    } catch (error) {
        console.error(
            "Unable to load environment information:",
            error
        );

        environmentLabel.textContent =
            "Environment Unknown";
    }
}

//
// Load pending-game counts
//
// Display format:
//
//     today / next 7 days
//
// Example:
//
//     2/8
//

async function loadPendingGames() {

    const badge =
        document.getElementById("pendingGames");

    if (!badge) {
        return;
    }

    try {

        const response =
            await fetch(
                "/api/games/pending-games/count",
                {
                    cache: "no-cache"
                }
            );

        if (!response.ok) {
            throw new Error(
                `Unable to load pending-game counts: HTTP ${response.status}`
            );
        }

        const result =
            await response.json();

        const todaysCount =
            Number(result.todaysCount ?? 0);
        
        const tomorrowsCount =
            Number(result.tomorrowsCount ?? 0);

        const sevenDayCount =
            Number(result.sevenDayCount ?? 0);

        const todayText =
            `${todaysCount} pending ` +
            `game${todaysCount === 1 ? "" : "s"} today`;

       const tomorrowText =
            `${tomorrowsCount} pending ` +
            `game${tomorrowsCount === 1 ? "" : "s"} tomorrow`;

        const sevenDayText =
            `${sevenDayCount} total pending ` +
            `game${sevenDayCount === 1 ? "" : "s"} ` +
            `through the next 7 days`;

        badge.textContent =
            `${todaysCount}/${tomorrowsCount}/${sevenDayCount}`;

 
        badge.title =
            `${todayText} / ${tomorrowText} / ${sevenDayText}`;

        badge.setAttribute(
            "aria-label",
            badge.title
        );

    } catch (error) {

        console.error(
            "Unable to load pending-game counts:",
            error
        );

        badge.textContent =
            "—";

        badge.title =
            "Unable to load pending-game counts";

        badge.setAttribute(
            "aria-label",
            badge.title
        );

    }

}

//
// Make the pending-games function available to other pages.
//
// A page can refresh the navbar count after adding,
// updating, or deleting a game by calling:
//
//     await loadPendingGames();
//

window.loadPendingGames =
    loadPendingGames;

//
// Format roles such as:
//
//     site_admin  -> Site Admin
//     head-admin  -> Head Admin
//

function formatRole(role) {

    if (!role) {
        return "";
    }

    return String(role)
        .replaceAll("_", " ")
        .replaceAll("-", " ")
        .replace(
            /\b\w/g,
            function (character) {
                return character.toUpperCase();
            }
        );

}

//
// Initialize logout
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

//
// Log out the current user
//

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

        alert("Logout failed. Please try again.");

    }

}