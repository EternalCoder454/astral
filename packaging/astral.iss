; Inno Setup script for Astral on Windows.
;
; Everything it packages comes out of packaging/windows-bundle.sh: the binary,
; the GTK and libadwaita DLLs, the GSettings schemas, the pixbuf loaders and an
; icon theme. A GTK application is not one file, and an installer that ships
; only the executable produces a program that will not start.
;
; Per-user by default. Astral needs no elevation, writes only into the user's
; own directories, and asking for an administrator to install a roleplay app is
; a worse trade than living in %LocalAppData%.

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif

#define AppName "Astral"
#define AppPublisher "Astral"
#define AppURL "https://github.com/EternalCoder454/astral"
#define AppExe "astral.exe"

[Setup]
AppId={{9C7F2E41-6B3D-4A58-9E0C-2F8A1D5B7C36}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
AppUpdatesURL={#AppURL}/releases
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
OutputDir=output
OutputBaseFilename=astral-{#AppVersion}-windows-x64-setup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
UninstallDisplayName={#AppName} {#AppVersion}
UninstallDisplayIcon={app}\{#AppExe}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Shortcuts:"

[Files]
Source: "..\dist\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\{#AppName}"; Filename: "{app}\{#AppExe}"
Name: "{group}\Uninstall {#AppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExe}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#AppExe}"; Description: "Start {#AppName}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; The settings and the library are the user's, and are left alone. This is
; only the icon cache the pixbuf loaders write next to the installation.
Type: filesandordirs; Name: "{app}\lib\gdk-pixbuf-2.0"
