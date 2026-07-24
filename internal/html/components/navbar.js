//
// Ref Ledger Shared Navbar
//

document.addEventListener("DOMContentLoaded", async function () {

    const container =
        document.getElementById("navbar-container");

    if (!container)
        return;

    try {

        const response =
            await fetch("/components/navbar.html");

        if (!response.ok)
            throw new Error("Unable to load navbar.");

        container.innerHTML =
            await response.text();

        initializeNavbar();

        highlightCurrentPage();

        loadUser();

        loadGamesToday();

    }
    catch (err) {

        console.error(err);

    }

});

//
// Initialize dropdown menu
//

function initializeNavbar() {

    const button =
        document.getElementById("userMenuButton");

    const menu =
        document.getElementById("userDropdown");

    if (!button || !menu)
        return;

    button.addEventListener("click", function (e) {

        e.stopPropagation();

        const expanded =
            button.getAttribute("aria-expanded") === "true";

        button.setAttribute(
            "aria-expanded",
            !expanded
        );

        menu.hidden = expanded;

    });

    menu.addEventListener("click", function (e) {

        e.stopPropagation();

    });

    document.addEventListener("click", closeMenu);

    document.addEventListener("keydown", function (e) {

        if (e.key === "Escape")
            closeMenu();

    });

    const logout =
        document.getElementById("logoutBtn");

    if (logout)
        logout.addEventListener("click", logoutUser);

}

function closeMenu() {

    const button =
        document.getElementById("userMenuButton");

    const menu =
        document.getElementById("userDropdown");

    if (!button || !menu)
        return;

    button.setAttribute(
        "aria-expanded",
        "false"
    );

    menu.hidden = true;

}

//
// Highlight active page
//

function highlightCurrentPage() {

    let page =
        window.location.pathname;

    if (page === "/")
        page = "home";
    else
        page = page.replace("/", "");

    const link =
        document.querySelector(
            '[data-page="' + page + '"]'
        );

    if (link)
        link.classList.add("active");

}

//
// Load logged in user
//

async function loadUser() {

    const userName =
        document.getElementById("userName");

    const userRole =
        document.getElementById("userRole");

    try {

        const response =
            await fetch("/api/session");

        if (response.status === 401) {

            window.location.href = "/login";

            return;

        }

        if (!response.ok)
            throw new Error();

        const session =
            await response.json();

        userName.textContent =
            session.name ||
            session.username ||
            session.email ||
            "Unknown User";

        userRole.textContent =
            formatRole(session.role);

    }
    catch (err) {

        userName.textContent =
            "Unknown User";

        userRole.textContent =
            "";

    }

}

function formatRole(role) {

    if (!role)
        return "";

    return role
        .replaceAll("_", " ")
        .replaceAll("-", " ")
        .replace(/\b\w/g, c => c.toUpperCase());

}

//
// Games today
//

async function loadGamesToday() {

    const count =
        document.getElementById("gamesTodayCount");

    if (!count)
        return;

    const today = getToday();

    const params =
        new URLSearchParams({
            begindate: today,
            enddate: today
        });

    try {

        const response =
            await fetch(
                "/api/dashboard?" + params.toString()
            );

        if (response.status === 404) {
            count.textContent = "0";
            return;
        }

        if (!response.ok)
            throw new Error(
                `Unable to load today's games: ${response.status}`
            );

        const result =
            await response.json();

        count.textContent =
            gameCount(result);

    } catch (error) {

        console.error(
            "Unable to load today's game count:",
            error
        );

        count.textContent = "0";
    }
}

function gameCount(result) {

    if (Array.isArray(result))
        return result.length;

    if (Array.isArray(result.games))
        return result.games.length;

    if (Array.isArray(result.data))
        return result.data.length;

    if (typeof result.count === "number")
        return result.count;

    return 0;

}

function getToday() {

    const d = new Date();

    const y = d.getFullYear();

    const m =
        String(d.getMonth() + 1)
            .padStart(2, "0");

    const day =
        String(d.getDate())
            .padStart(2, "0");

    return `${y}-${m}-${day}`;

}

//
// Logout
//

async function logoutUser() {

    try {

        const response =
            await fetch("/api/logout", {

                method: "POST"

            });

        if (response.ok)
            window.location.href = "/login";
        else
            alert("Logout failed.");

    }
    catch {

        alert("Logout failed.");

    }

}