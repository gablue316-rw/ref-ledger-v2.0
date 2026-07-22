console.log("user-profile.js loaded");

async function loadUserProfile() {


    try {


        const response =
            await fetch("/api/session");


        console.log("Status:", response.status);
       
        const user =
            await response.json();

        console.log("User:", user);
        
        document
        .getElementById("user-name")
        .innerText =
            user.name;


        document
        .getElementById("user-role")
        .innerText =
            user.role;


        document
        .getElementById("user-avatar")
        .innerText =
            getInitials(user.name);


    }
    catch(err) {

        console.error(
            "User profile error",
            err
        );

    }

}



function getInitials(name) {

    if (!name) {
        return "?";
    }


    let parts =
        name
        .replace("@", " ")
        .replace(".", " ")
        .split(" ");


    let first =
        parts[0] ? parts[0][0] : "";


    let second =
        parts.length > 1 && parts[1]
            ? parts[1][0]
            : "";


    return (
        first + second
    ).toUpperCase();

}



document.addEventListener(
    "DOMContentLoaded",
    loadUserProfile
);console.log("user-profile.js loaded");


async function loadUserProfile() {

    try {

        const response =
            await fetch("/api/session", {
                credentials: "include"
            });


        console.log("Status:", response.status);


        if (!response.ok) {
            console.error(
                "Session request failed:",
                response.status
            );
            return;
        }


        const user =
            await response.json();


        console.log("User:", user);


        document
        .getElementById("user-name")
        .innerText =
            user.name;


        document
        .getElementById("user-role")
        .innerText =
            user.role;


        document
        .getElementById("user-avatar")
        .innerText =
            getInitials(user.name);


    }
    catch(err) {

        console.error(
            "User profile error",
            err
        );

    }

}



function getInitials(name) {

    if (!name) {
        return "?";
    }


    let parts =
        name
        .replace("@", " ")
        .replace(".", " ")
        .split(" ");


    let first =
        parts[0] ? parts[0][0] : "";


    let second =
        parts.length > 1 && parts[1]
            ? parts[1][0]
            : "";


    return (
        first + second
    ).toUpperCase();

}



document.addEventListener(
    "DOMContentLoaded",
    loadUserProfile
);