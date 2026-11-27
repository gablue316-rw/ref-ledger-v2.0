// Ref Ledger shared navbar

document.addEventListener("DOMContentLoaded", initializeNavbar);

// Load and initialize the shared navbar.
async function initializeNavbar() {
    const container = document.getElementById("navbar-container");

    if (!container) {
        return;
    }

    try {
        const response = await fetch("/components/navbar.html", {
            cache: "no-cache"
        });

        if (!response.ok) {
            throw new Error(`Unable to load navbar: HTTP ${response.status}`);
        }

        container.innerHTML = await response.text();

        initializeSidebar();
        highlightCurrentPage();
        initializeLogout();
        initializeAnnouncements(container);

        await Promise.all([
            loadCurrentUser(),
            loadPendingGames(),
            loadEnvironment()
        ]);
    } catch (error) {
        console.error("Unable to initialize navbar:", error);
    }
}

// Highlight the current navigation link.
function highlightCurrentPage() {
    const pathname = window.location.pathname;
    let page;

    if (pathname === "/" || pathname === "/dashboard") {
        page = "dashboard";
    } else if (pathname === "/home") {
        page = "home";
    } else {
        page = pathname
            .replace(/^\/+/, "")
            .replace(/\/+$/, "");
    }

    const navigationLinks = document.querySelectorAll(
        ".nav-links a[data-page]"
    );

    navigationLinks.forEach(function (link) {
        const linkPage = link.getAttribute("data-page");
        link.classList.toggle("active", linkPage === page);
        if (linkPage === page) link.setAttribute("aria-current", "page");
        else link.removeAttribute("aria-current");
    });
}

// Load the logged-in user.
async function loadCurrentUser() {
    const userName = document.getElementById("userName");
    const userRole = document.getElementById("userRole");

    if (!userName || !userRole) {
        return;
    }

    try {
        const response = await fetch("/api/session", {
            cache: "no-cache"
        });

        if (response.status === 401) {
            window.location.href = "/login";
            return;
        }

        if (!response.ok) {
            throw new Error(`Unable to load session: HTTP ${response.status}`);
        }

        const session = await response.json();

        userName.textContent =
            session.name ||
            session.username ||
            session.email ||
            "Unknown User";

        userRole.textContent = formatRole(session.role);
    } catch (error) {
        console.error("Unable to load user information:", error);
        userName.textContent = "Unknown User";
        userRole.textContent = "";
    }
}

// Load environment information.
async function loadEnvironment() {
    const navbar = document.getElementById("main-navbar");
    const environmentLabel = document.getElementById("environment-label");

    if (!navbar || !environmentLabel) {
        return;
    }

    try {
        const response = await fetch("/api/environment", {
            cache: "no-cache"
        });

        if (!response.ok) {
            throw new Error(`Unable to load environment: HTTP ${response.status}`);
        }

        const environmentInfo = await response.json();
        const environment = String(environmentInfo.environment || "")
            .trim()
            .toLowerCase();

        if (environment !== "development") {
            navbar.classList.remove("navbar-development");
            environmentLabel.replaceChildren();
            environmentLabel.hidden = true;
            return;
        }

        navbar.classList.add("navbar-development");
        environmentLabel.hidden = false;
        environmentLabel.replaceChildren();

        [
            environmentInfo.environment || "Unknown",
            environmentInfo.hostLabel || "Unknown Host",
            environmentInfo.database || "Unknown Database",
            environmentInfo.release || "Unknown Release"
        ].forEach(function (value) {
            const line = document.createElement("div");
            line.textContent = value;
            environmentLabel.appendChild(line);
        });
    } catch (error) {
        console.error("Unable to load environment information:", error);
        environmentLabel.replaceChildren();
        environmentLabel.hidden = true;
    }
}

// Load pending-game counts.
// Display format: today / tomorrow / next 7 days.
// Example: 2/3/8.
async function loadPendingGames() {
    const badge = document.getElementById("pendingGames");

    if (!badge) {
        return;
    }

    try {
        const response = await fetch("/api/games/pending-games/count", {
            cache: "no-cache"
        });

        if (!response.ok) {
            throw new Error(
                `Unable to load pending-game counts: HTTP ${response.status}`
            );
        }

        const result = await response.json();
        const todaysCount = Number(result.todaysCount ?? 0);
        const tomorrowsCount = Number(result.tomorrowsCount ?? 0);
        const sevenDayCount = Number(result.sevenDayCount ?? 0);

        const todayText =
            `${todaysCount} pending ` +
            `game${todaysCount === 1 ? "" : "s"} today`;

        const tomorrowText =
            `${tomorrowsCount} pending ` +
            `game${tomorrowsCount === 1 ? "" : "s"} tomorrow`;

        const sevenDayText =
            `${sevenDayCount} total pending ` +
            `game${sevenDayCount === 1 ? "" : "s"} ` +
            "through the next 7 days";

        badge.textContent =
            `${todaysCount}/${tomorrowsCount}/${sevenDayCount}`;

        badge.title =
            `${todayText} / ${tomorrowText} / ${sevenDayText}`;

        badge.setAttribute("aria-label", badge.title);
    } catch (error) {
        console.error("Unable to load pending-game counts:", error);
        badge.textContent = "—";
        badge.title = "Unable to load pending-game counts";
        badge.setAttribute("aria-label", badge.title);
    }
}

// Make the pending-games function available to other pages.
// A page can refresh the count with: await loadPendingGames();
window.loadPendingGames = loadPendingGames;

// Format roles such as site_admin -> Site Admin
// and head-admin -> Head Admin.
function formatRole(role) {
    if (!role) {
        return "";
    }

    return String(role)
        .replaceAll("_", " ")
        .replaceAll("-", " ")
        .replace(/\b\w/g, function (character) {
            return character.toUpperCase();
        });
}

// Initialize logout.
function initializeLogout() {
    const logoutButton = document.getElementById("logoutBtn");

    if (!logoutButton) {
        return;
    }

    logoutButton.addEventListener("click", logoutUser);
}

// Log out the current user.
async function logoutUser(event) {
    event.preventDefault();

    try {
        const response = await fetch("/api/logout", {
            method: "POST"
        });

        if (!response.ok) {
            throw new Error(`Logout failed: HTTP ${response.status}`);
        }

        window.location.href = "/login";
    } catch (error) {
        console.error("Unable to log out:", error);
        alert("Logout failed. Please try again.");
    }
}

// Compact screens use a dismissible navigation drawer.
function initializeSidebar() {
    document.body.classList.add("has-ledger-sidebar");
    const toggle = document.querySelector(".sidebar-toggle");
    const backdrop = document.querySelector(".sidebar-backdrop");
    const nav = document.getElementById("main-navbar");
    const media = window.matchMedia("(max-width: 800px)");
    function setOpen(open, returnFocus = false) {
        document.body.classList.toggle("ledger-sidebar-open", open);
        toggle.setAttribute("aria-expanded", String(open));
        backdrop.hidden = !open;
        nav.inert = media.matches && !open;
        if (open) nav.querySelector("a").focus();
        if (returnFocus) toggle.focus();
    }
    toggle.addEventListener("click", () => setOpen(toggle.getAttribute("aria-expanded") !== "true"));
    backdrop.addEventListener("click", () => setOpen(false, true));
    document.addEventListener("keydown", event => {
        if (event.key === "Escape" && toggle.getAttribute("aria-expanded") === "true") setOpen(false, true);
        if (event.key === "Tab" && media.matches && toggle.getAttribute("aria-expanded") === "true") {
            const items = [toggle, ...nav.querySelectorAll("a[href], button:not([disabled])")];
            const first = items[0], last = items[items.length - 1];
            if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
            else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
        }
    });
    nav.addEventListener("click", event => { if (event.target.closest("a") && media.matches) setOpen(false); });
    media.addEventListener("change", () => setOpen(false));
    setOpen(false);
}


// ---- Announcement banner ----
// Plain text only (textContent). Live updates arrive over Server-Sent Events;
// the server sends the full current state on every (re)connect.
function initializeAnnouncements(container) {
    if (document.getElementById("announcement-banner") || !window.EventSource) {
        return;
    }

    const style = document.createElement("style");
    style.textContent = `
        #announcement-banner{display:flex;align-items:center;gap:12px;padding:8px 16px;background:#ffd23f;color:#1a1200;
            font:600 14px/1.4 "Segoe UI",Arial,sans-serif;border-bottom:2px solid #b38600}
        #announcement-banner[hidden]{display:none}
        #announcement-banner .announcement-text{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
        #announcement-banner button{flex:none;background:#1a1200;color:#fff;border:0;border-radius:4px;padding:4px 10px;font:inherit;cursor:pointer}
        #announcement-dialog{max-width:min(640px,92vw);border:2px solid #b38600;border-radius:8px;padding:20px;color:#1a1200}
        #announcement-dialog .announcement-full{max-height:60vh;overflow:auto;white-space:pre-wrap;overflow-wrap:anywhere;margin:0 0 16px}
        #announcement-dialog button{background:#132d3d;color:#fff;border:0;border-radius:4px;padding:8px 16px;font:inherit;cursor:pointer}`;
    document.head.appendChild(style);

    const banner = document.createElement("div");
    banner.id = "announcement-banner";
    banner.setAttribute("role", "status");
    banner.hidden = true;
    const text = document.createElement("span");
    text.className = "announcement-text";
    const more = document.createElement("button");
    more.type = "button";
    more.textContent = "Read more";
    more.hidden = true;
    banner.append(text, more);
    container.insertAdjacentElement("afterend", banner);

    const dialog = document.createElement("dialog");
    dialog.id = "announcement-dialog";
    dialog.setAttribute("aria-labelledby", "announcement-dialog-title");
    dialog.innerHTML = '<h2 id="announcement-dialog-title" style="margin-top:0;font-size:18px">Announcement</h2>' +
        '<p class="announcement-full" tabindex="0"></p><button type="button">Close</button>';
    document.body.appendChild(dialog);
    const full = dialog.querySelector(".announcement-full");
    dialog.querySelector("button").addEventListener("click", () => dialog.close());

    const updateOverflow = () => {
        more.hidden = true;
        if (!banner.hidden) {
            more.hidden = !(text.scrollWidth > text.clientWidth || text.textContent.includes("\n"));
        }
    };
    more.addEventListener("click", () => {
        full.textContent = text.textContent;
        if (!dialog.open) {
            dialog.showModal();
        }
    });
    window.addEventListener("resize", updateOverflow);

    const show = (announcement) => {
        if (!announcement || !announcement.message) {
            if (dialog.open) {
                dialog.close();
            }
            banner.hidden = true;
            text.textContent = "";
            more.hidden = true;
            return;
        }
        text.textContent = announcement.message;
        banner.hidden = false;
        updateOverflow();
        if (dialog.open) {
            full.textContent = announcement.message;
        }
    };

    const source = new EventSource("/api/announcements/events");
    source.addEventListener("announcement", (event) => {
        try {
            show(JSON.parse(event.data).announcement);
        } catch (error) {
            console.error("Unable to read announcement:", error);
        }
    });
    source.addEventListener("session_ended", () => {
        source.close();
        show(null);
    });
}
