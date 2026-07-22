async function loadUserProfile() {


    try {


        const response =
            await fetch("/api/session");


        if (!response.ok) {
            return;
        }


        const user =
            await response.json();


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


    if (!name)
        return "?";


    let parts =
        name
        .replace("@"," ")
        .replace("."," ")
        .split(" ");


    return (
        parts[0][0] +
        (parts[1]?.[0] || "")
    )
    .toUpperCase();

}



document.addEventListener(
    "DOMContentLoaded",
    loadUserProfile
);