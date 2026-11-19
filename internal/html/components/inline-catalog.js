(function () {
    "use strict";

    const names = {
        official: "Official",
        site: "Site",
        assignor: "Assignor",
        association: "Association",
        level: "Level",
        sport: "Sport"
    };
    const formHTML = {
        official: `
            <label>First name<input name="firstName" required></label>
            <label>Last name<input name="lastName" required></label>
            <label>Phone<input name="phone" type="tel"></label>
            <label>Email<input name="email" type="email"></label>
            <label>Address<input name="address"></label>`,
        site: `
            <label>Site name<input name="name" required></label>
            <label>Contact name<input name="contact"></label>
            <label>Phone<input name="phone" type="tel"></label>
            <label>Email<input name="email" type="email"></label>`,
        assignor: `
            <label>Assignor name<input name="name" required></label>`,
        association: `
            <label>Association ID<input name="id" required></label>
            <label>Association name<input name="name" required></label>
            <label>Contact name<input name="contact"></label>
            <label>Phone<input name="phone" type="tel"></label>
            <label>Email<input name="email" type="email"></label>`,
        level: `
            <label>Sport<select name="sport" required></select></label>
            <label>Level name<input name="name" required maxlength="50"></label>
            <label>Game fee<input name="gameFee" type="number" min="0" step="0.01" value="0.00" required></label>`,
        sport: `
            <p class="inline-catalog-help">Select a sport active in the shared catalog to activate it for this account.</p>
            <label>Sport<select name="sport" required></select></label>`
    };

    const style = document.createElement("style");
    style.textContent = `
        .inline-catalog-action {
            display: inline-block;
            margin-top: 6px;
            padding: 4px 8px;
            border: 1px solid #2563eb;
            border-radius: 5px;
            background: #fff;
            color: #1d4ed8;
            font: inherit;
            font-size: .85rem;
            cursor: pointer;
        }
        .inline-catalog-action:hover { background: #eff6ff; }
        #inlineCatalogDialog {
            width: min(460px, calc(100% - 32px));
            max-height: 85vh;
            border: 0;
            border-radius: 12px;
            padding: 22px;
            box-shadow: 0 18px 55px #0f172a55;
        }
        #inlineCatalogDialog::backdrop { background: #0f172a88; }
        #inlineCatalogDialog h2 { margin: 0 0 14px; }
        #inlineCatalogFields { display: grid; gap: 12px; }
        #inlineCatalogFields label { display: grid; gap: 5px; font-weight: 600; }
        #inlineCatalogFields input, #inlineCatalogFields select {
            box-sizing: border-box;
            width: 100%;
            min-height: 38px;
            padding: 7px 9px;
            border: 1px solid #94a3b8;
            border-radius: 5px;
            font: inherit;
            font-weight: 400;
        }
        .inline-catalog-help { margin: 0; color: #475569; }
        #inlineCatalogError { min-height: 1.2em; margin: 12px 0 0; color: #a32020; }
        .inline-catalog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 14px; }
    `;
    document.head.appendChild(style);

    const dialog = document.createElement("dialog");
    dialog.id = "inlineCatalogDialog";
    dialog.setAttribute("aria-labelledby", "inlineCatalogTitle");
    dialog.innerHTML = `
        <form id="inlineCatalogForm">
            <h2 id="inlineCatalogTitle"></h2>
            <div id="inlineCatalogFields"></div>
            <p id="inlineCatalogError" role="alert"></p>
            <div class="inline-catalog-actions">
                <button type="button" id="inlineCatalogCancel">Cancel</button>
                <button type="submit" id="inlineCatalogSave">Save</button>
                <button type="button" id="inlineCatalogActivate" hidden></button>
            </div>
        </form>`;
    document.body.appendChild(dialog);

    const form = document.getElementById("inlineCatalogForm");
    const fields = document.getElementById("inlineCatalogFields");
    const title = document.getElementById("inlineCatalogTitle");
    const errorBox = document.getElementById("inlineCatalogError");
    const saveButton = document.getElementById("inlineCatalogSave");
    const activateButton = document.getElementById("inlineCatalogActivate");
    let activeType = "";
    let originSelect = null;
    let pendingLevel = null;
    let busy = false;

    function catalogType(select) {
        const fixed = {
            association: "association",
            sport: "sport",
            level: "level",
            site: "site",
            assignor: "assignor",
            editSport: "sport",
            editLevel: "level",
            editSite: "site",
            editAssignor: "assignor"
        };
        if (fixed[select.id]) return fixed[select.id];
        if (select.matches("#officialFields select, #editOfficialsFields select")) return "official";
        return "";
    }

    function decorateControls() {
        document.querySelectorAll("select").forEach(select => {
            const type = catalogType(select);
            if (!type || select.dataset.inlineCatalogReady === "true" || !select.parentElement) return;
            if (type === "official" && select.disabled) return;
            select.dataset.inlineCatalogReady = "true";
            const button = document.createElement("button");
            button.type = "button";
            button.className = "inline-catalog-action";
            button.dataset.inlineCatalogType = type;
            button.textContent = `+ New ${names[type]}`;
            select.insertAdjacentElement("afterend", button);
        });
    }

    const observer = new MutationObserver(decorateControls);
    observer.observe(document.body, { childList: true, subtree: true });
    decorateControls();

    function selectedAssociation(select) {
        if (select.id === "association") return select.value.trim();
        if (select.id === "editAssociation") return select.value.trim();
        const editAssociation = document.getElementById("editAssociation");
        if (editAssociation) return editAssociation.value.trim();
        return document.getElementById("association")?.value.trim() || "";
    }

    function selectedSport(select) {
        if (select.id === "sport" || select.id === "editSport") return select.value.trim();
        return document.getElementById(select.id.startsWith("edit") ? "editSport" : "sport")?.value.trim() || "";
    }

    async function readResponse(response) {
        const text = await response.text();
        if (response.status === 401 || response.redirected) {
            window.location.href = "/login";
            throw new Error("Your session has expired. Sign in again.");
        }
        if (response.status === 403) {
            throw new Error("Permission denied. Read-only users cannot create or activate catalog records.");
        }
        if (!response.ok) throw new Error(text.trim() || `Request failed (HTTP ${response.status}).`);
        return text;
    }

    async function requestJSON(url) {
        const response = await fetch(url, { cache: "no-store" });
        const text = await readResponse(response);
        return text ? JSON.parse(text) : null;
    }

    async function post(url, payload) {
        return readResponse(await fetch(url, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(payload)
        }));
    }

    function setError(message) {
        errorBox.textContent = message || "";
    }

    function toggleBusy(value) {
        busy = value;
        document.getElementById("inlineCatalogCancel").disabled = value;
        saveButton.disabled = value;
        activateButton.disabled = value;
    }

    function closeModal(force = false) {
        if (busy && !force) return;
        dialog.close();
        form.reset();
        fields.replaceChildren();
        pendingLevel = null;
        setError("");
        activateButton.hidden = true;
        saveButton.hidden = false;
        if (originSelect?.isConnected) originSelect.focus();
        originSelect = null;
    }

    document.getElementById("inlineCatalogCancel").addEventListener("click", closeModal);
    dialog.addEventListener("cancel", event => {
        if (busy) {
            event.preventDefault();
            return;
        }
        closeModal();
    });
    dialog.addEventListener("click", event => {
        if (event.target === dialog) closeModal();
    });

    function addOptions(select, values, placeholder) {
        select.replaceChildren(new Option(placeholder, ""));
        values.forEach(value => select.add(new Option(value, value)));
    }

    async function loadSportOptions(type, currentSport) {
        const select = form.elements.sport;
        const supported = await requestJSON("/api/loadSportsSupported") || [];
        const allSports = await requestJSON("/api/sports?active=true") || [];
        if (!Array.isArray(supported) || !Array.isArray(allSports)) {
            throw new Error("Unable to load sport choices.");
        }
        const available = type === "sport"
            ? allSports.map(item => String(item.name ?? item.Name ?? "").trim())
                .filter(name => name && !supported.includes(name))
            : supported.map(name => String(name).trim()).filter(Boolean);
        addOptions(select, [...new Set(available)], available.length ? "Select Sport" : "No available sports");
        if (currentSport && available.includes(currentSport)) select.value = currentSport;
        if (!available.length) {
            setError(type === "sport"
                ? "No inactive-for-this-account sports are available. An administrator must activate a sport in the shared catalog first."
                : "Activate a sport for this account before creating a level.");
        }
    }

    async function openModal(button) {
        activeType = button.dataset.inlineCatalogType;
        originSelect = button.previousElementSibling;
        if (!(originSelect instanceof HTMLSelectElement)) return;
        pendingLevel = null;
        form.reset();
        fields.innerHTML = formHTML[activeType];
        title.textContent = `New ${names[activeType]}`;
        saveButton.textContent = activeType === "sport" ? "Activate & select" : "Save";
        saveButton.hidden = false;
        activateButton.hidden = true;
        setError("");
        if (activeType === "assignor" && !selectedAssociation(originSelect)) {
            setError("Select or create an association first.");
        }
        dialog.showModal();
        const firstInput = fields.querySelector("input, select");
        firstInput?.focus();
        try {
            if (activeType === "level" || activeType === "sport") {
                await loadSportOptions(activeType, selectedSport(originSelect));
            }
        } catch (error) {
            setError(error.message);
        }
    }

    document.addEventListener("click", event => {
        const button = event.target.closest(".inline-catalog-action");
        if (!button) return;
        event.preventDefault();
        openModal(button);
    });

    function formValues() {
        return Object.fromEntries(new FormData(form).entries());
    }

    function selectOption(select, value, label = value) {
        let option = Array.from(select.options).find(item => item.value === value);
        if (!option) {
            option = new Option(label, value);
            select.add(option);
        }
        select.value = value;
        return option;
    }

    function appendOfficialToGameForm(official) {
        const fullName = `${official.firstName} ${official.lastName}`.trim();
        const selects = originSelect.closest("#gameForm, #editGameForm")
            ?.querySelectorAll("#officialFields select, #editOfficialsFields select") || [originSelect];
        selects.forEach(select => {
            const existing = Array.from(select.options).find(option =>
                option.dataset.name?.trim().toLowerCase() === fullName.toLowerCase());
            if (existing) return;
            const option = new Option(fullName, `inline:${official.id || fullName}`);
            option.dataset.officialId = official.id || "";
            option.dataset.name = fullName;
            select.add(option);
        });
        const selectedOption = Array.from(originSelect.options).find(option =>
            option.dataset.name?.trim().toLowerCase() === fullName.toLowerCase());
        if (selectedOption) originSelect.value = selectedOption.value;
    }

    async function refreshOfficials(values) {
        const query = new URLSearchParams({ firstname: values.firstName, lastname: values.lastName });
        const records = await requestJSON(`/api/officialsDirectory?${query}`);
        const matches = Array.isArray(records) ? records : [];
        const official = matches.find(item =>
            String(item.firstName ?? item.FirstName ?? "").trim().toLowerCase() === values.firstName.toLowerCase() &&
            String(item.lastName ?? item.LastName ?? "").trim().toLowerCase() === values.lastName.toLowerCase());
        appendOfficialToGameForm({
            firstName: values.firstName,
            lastName: values.lastName,
            id: official ? String(official.officialId ?? official.OfficialId ?? official.id ?? official.Id ?? "") : ""
        });
    }

    async function refreshCatalog(type, values) {
        if (type === "site") {
            const records = await requestJSON("/api/loadSites");
            if (!Array.isArray(records)) throw new Error("Unable to refresh sites.");
            const sites = records.map(item => String(
                typeof item === "string" ? item : item.name ?? item.Name ?? ""
            ).trim()).filter(Boolean);
            if (typeof dashboardSites !== "undefined") {
                dashboardSites = sites.map(name => ({ name }));
            }
            const placeholder = originSelect.options[0]?.textContent || "Select Site";
            addOptions(originSelect, [...new Set(sites)], placeholder);
            selectOption(originSelect, values.name);
            return;
        }
        if (type === "association") {
            const records = await requestJSON("/api/loadAssociations");
            if (!Array.isArray(records)) throw new Error("Unable to refresh associations.");
            const associations = records.map(item => ({
                id: String(item.id ?? item.Id ?? "").trim(),
                name: String(item.name ?? item.Name ?? "").trim()
            })).filter(item => item.id);
            const placeholder = originSelect.options[0]?.textContent || "Select Association";
            originSelect.replaceChildren(new Option(placeholder, ""));
            associations.forEach(item => originSelect.add(new Option(item.name || item.id, item.id)));
            selectOption(originSelect, values.id);
            const assignorSelect = document.getElementById(originSelect.id === "association"
                ? "assignor" : "editAssignor");
            if (assignorSelect) {
                assignorSelect.disabled = true;
                assignorSelect.replaceChildren(new Option("Loading assignors...", ""));
                const hint = document.getElementById("assignorHint");
                if (hint) hint.hidden = true;
                await refreshAssignors(values.id, "Unassigned", assignorSelect);
            }
            return;
        }
        if (type === "assignor") {
            await refreshAssignors(selectedAssociation(originSelect), values.name);
            selectOption(originSelect, values.name);
            return;
        }
        if (type === "official") {
            await refreshOfficials(values);
            return;
        }
    }

    async function refreshAssignors(association, selected = "Unassigned", select = originSelect) {
        if (!association) throw new Error("Select an association before loading assignors.");
        if (select.id === "assignor" && typeof assignorsVersion !== "undefined") assignorsVersion++;
        if (select.id === "editAssignor" && typeof editAssignorsVersion !== "undefined") editAssignorsVersion++;
        const params = new URLSearchParams({ association });
        const records = await requestJSON(`/api/loadAssignors?${params}`);
        if (!Array.isArray(records)) throw new Error("Unable to refresh assignors.");
        const names = records.map(record => String(
            typeof record === "string" ? record : record.name ?? record.Name ?? ""
        ).trim()).filter(Boolean);
        select.replaceChildren(new Option("Select Assignor", ""));
        [...new Set(names)].forEach(name => select.add(new Option(name, name)));
        if (selected === "Unassigned") {
            select.add(new Option("Unassigned", "Unassigned"));
        }
        selectOption(select, selected);
        select.disabled = false;
        if (select.id === "assignor" && typeof assignorsReady !== "undefined") assignorsReady = true;
        if (select.id === "editAssignor" && typeof editAssignorsReady !== "undefined") editAssignorsReady = true;
    }

    async function refreshLevels(sport, levelName) {
        if (typeof window.refreshDashboardEditLevels === "function") {
            await window.refreshDashboardEditLevels();
            if (typeof window.populateSportEditLevels === "function") {
                window.populateSportEditLevels(document.getElementById("editLevel").value, true);
            }
        } else if (typeof window.loadLevels === "function") {
            if (!await window.loadLevels()) throw new Error("Unable to refresh the level dropdown.");
        } else {
            throw new Error("Unable to refresh the level dropdown.");
        }
        const levelSelect = originSelect;
        selectOption(levelSelect, levelName);
        levelSelect.dispatchEvent(new Event("change", { bubbles: true }));
    }

    async function updateSupportedSports(sport) {
        const supported = await requestJSON("/api/loadSportsSupported") || [];
        if (!supported.includes(sport)) supported.push(sport);
        if (typeof dashboardUserSports !== "undefined") {
            dashboardUserSports = supported;
            dashboardUserSportsReady = true;
        }
        if (typeof userSports !== "undefined") {
            userSports = supported;
            userSportsReady = true;
        }
        if (!Array.from(originSelect.options).some(option => option.value === sport)) {
            originSelect.add(new Option(sport, sport));
        }
        originSelect.value = sport;
        originSelect.dispatchEvent(new Event("change", { bubbles: true }));
    }

    async function activateLevel(level) {
        await post("/api/level-active", level);
        await refreshLevels(level.sport, level.name);
        closeModal(true);
    }

    async function findMatchingLevel(name, sport) {
        const levels = await requestJSON(`/api/levels?sport=${encodeURIComponent(sport)}`);
        if (!Array.isArray(levels)) return null;
        return levels.find(level =>
            String(level.name ?? level.Name ?? "").trim().toLowerCase() === name.trim().toLowerCase() &&
            String(level.sport ?? level.Sport ?? "").trim() === sport) || null;
    }

    async function saveLevel(values) {
        const sport = String(values.sport || "").trim();
        const name = String(values.name || "").trim();
        if (!sport) throw new Error("Select an active sport before creating a level.");
        const fee = String(values.gameFee || "0.00");
        const response = await fetch("/api/levels", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ id: name, name, sport, gameFee: fee })
        });
        let created;
        try {
            const text = await readResponse(response);
            created = text ? JSON.parse(text) : null;
        } catch (error) {
            if (!/already exists|duplicate/i.test(error.message)) throw error;
            const matching = await findMatchingLevel(name, sport);
            if (matching && !(matching.active ?? matching.Active)) {
                pendingLevel = matching;
                saveButton.hidden = true;
                activateButton.hidden = false;
                activateButton.textContent = "Activate existing & select";
                setError(`${error.message} This matching level is inactive; activate it to use it.`);
                return;
            }
            throw error;
        }
        if (!created || !created.id || !created.version) {
            throw new Error("The new level was saved, but its activation details could not be loaded.");
        }
        pendingLevel = created;
        saveButton.hidden = true;
        activateButton.hidden = false;
        activateButton.textContent = "Activate & select";
        setError("Level saved as inactive. Activate it to select it for this game.");
    }

    async function submitForm() {
        if (busy || !form.reportValidity()) return;
        const values = formValues();
        setError("");
        toggleBusy(true);
        try {
            if (activeType === "sport") {
                const sport = String(values.sport || "").trim();
                if (!sport) throw new Error("Select an active sport.");
                await post("/api/sports_supported/activate", { sport });
                await updateSupportedSports(sport);
                closeModal(true);
                return;
            }
            if (activeType === "level") {
                await saveLevel(values);
                return;
            }
            if (activeType === "assignor") {
                const association = selectedAssociation(originSelect);
                if (!association) throw new Error("Select or create an association first.");
                await post("/api/assignors", { association, name: String(values.name).trim() });
            } else {
                const payload = Object.fromEntries(Object.entries(values).map(([key, value]) =>
                    [key, String(value).trim()]));
                const endpoint = {
                    official: "/api/officials",
                    site: "/api/sites",
                    association: "/api/associations"
                }[activeType];
                await post(endpoint, payload);
                await refreshCatalog(activeType, payload);
            }
            closeModal(true);
        } catch (error) {
            setError(error.message || `Unable to save ${names[activeType].toLowerCase()}.`);
        } finally {
            toggleBusy(false);
        }
    }

    form.addEventListener("submit", event => {
        event.preventDefault();
        submitForm();
    });

    activateButton.addEventListener("click", async () => {
        if (busy || !pendingLevel) return;
        toggleBusy(true);
        setError("");
        try {
            await activateLevel(pendingLevel);
        } catch (error) {
            setError(error.message || "Unable to activate the level.");
        } finally {
            toggleBusy(false);
        }
    });
})();
