// Inline stroke icons, sized/weighted to sit alongside Metronic's keenicons
// (18px nominal, 1.6 stroke, round caps). Kept here so every view draws from
// one set instead of scattering emoji through the markup.
import React from "react";

const S = {
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.6,
  strokeLinecap: "round",
  strokeLinejoin: "round",
};

const mk = (paths) => function Icon(props) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" {...props}>
      <g {...S}>{paths}</g>
    </svg>
  );
};

export const IconDevices = mk(<>
  <rect x="5" y="2.5" width="14" height="19" rx="2.5" />
  <path d="M10.5 18.5h3" />
</>);

export const IconGroups = mk(<>
  <path d="M3 7.5a2 2 0 0 1 2-2h3.6a2 2 0 0 1 1.5.7l.9 1.1H19a2 2 0 0 1 2 2v7.2a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
</>);

export const IconEnroll = mk(<>
  <circle cx="12" cy="12" r="9" />
  <path d="M12 8.2v7.6M8.2 12h7.6" />
</>);

export const IconPackage = mk(<>
  <path d="M20.5 7.8v8.4a1.6 1.6 0 0 1-.85 1.42l-6.9 3.7a1.6 1.6 0 0 1-1.5 0l-6.9-3.7A1.6 1.6 0 0 1 3.5 16.2V7.8a1.6 1.6 0 0 1 .85-1.42l6.9-3.7a1.6 1.6 0 0 1 1.5 0l6.9 3.7A1.6 1.6 0 0 1 20.5 7.8z" />
  <path d="m3.8 7 8.2 4.4L20.2 7M12 21v-9.6" />
</>);

export const IconSignOut = mk(<>
  <path d="M14.5 8V5.8A1.8 1.8 0 0 0 12.7 4H6.3a1.8 1.8 0 0 0-1.8 1.8v12.4A1.8 1.8 0 0 0 6.3 20h6.4a1.8 1.8 0 0 0 1.8-1.8V16" />
  <path d="M20 12H10m10 0-3-3m3 3-3 3" />
</>);

export const IconChevronLeft = mk(<path d="m14.5 5.5-6 6.5 6 6.5" />);

export const IconRefresh = mk(<>
  <path d="M20 12a8 8 0 1 1-2.4-5.7" />
  <path d="M20.2 4v4.2H16" />
</>);

export const IconPlus = mk(<path d="M12 5.5v13M5.5 12h13" />);

export const IconTrash = mk(<>
  <path d="M4.5 6.8h15M9.8 6.8V5.4A1.4 1.4 0 0 1 11.2 4h1.6a1.4 1.4 0 0 1 1.4 1.4v1.4" />
  <path d="M6.6 6.8 7.4 19a1.5 1.5 0 0 0 1.5 1.4h6.2a1.5 1.5 0 0 0 1.5-1.4l.8-12.2" />
</>);

export const IconEdit = mk(<>
  <path d="M4.5 19.5h4l10-10a2.1 2.1 0 0 0-3-3l-10 10z" />
  <path d="M14.2 5.8 18.2 9.8" />
</>);

export const IconPower = mk(<>
  <path d="M12 3.5v8" />
  <path d="M6.6 6.6a7.5 7.5 0 1 0 10.8 0" />
</>);

export const IconLock = mk(<>
  <rect x="5" y="10.5" width="14" height="10" rx="2.2" />
  <path d="M8.2 10.5V7.8a3.8 3.8 0 0 1 7.6 0v2.7" />
</>);

export const IconUnlock = mk(<>
  <rect x="5" y="10.5" width="14" height="10" rx="2.2" />
  <path d="M8.2 10.5V7.8a3.8 3.8 0 0 1 7.3-1.4" />
</>);

export const IconEject = mk(<>
  <circle cx="12" cy="12" r="9" />
  <path d="m9 9 6 6m0-6-6 6" />
</>);

export const IconCopy = mk(<>
  <rect x="9" y="9" width="11" height="11" rx="2" />
  <path d="M15 6.5A2.5 2.5 0 0 0 12.5 4h-6A2.5 2.5 0 0 0 4 6.5v6A2.5 2.5 0 0 0 6.5 15" />
</>);

export const IconCheck = mk(<path d="m5 12.5 4.5 4.5L19 7.5" />);

export const IconUpload = mk(<>
  <path d="M4.5 15.5v2.7A2.3 2.3 0 0 0 6.8 20.5h10.4a2.3 2.3 0 0 0 2.3-2.3v-2.7" />
  <path d="M12 3.5v11M7.8 7.7 12 3.5l4.2 4.2" />
</>);

export const IconInfo = mk(<>
  <circle cx="12" cy="12" r="9" />
  <path d="M12 11v5.2M12 7.9v.1" />
</>);

export const IconWarning = mk(<>
  <path d="M10.6 4.3 2.9 17.6a1.6 1.6 0 0 0 1.4 2.4h15.4a1.6 1.6 0 0 0 1.4-2.4L13.4 4.3a1.6 1.6 0 0 0-2.8 0z" />
  <path d="M12 9.5v4.2M12 16.8v.1" />
</>);

export const IconFile = mk(<>
  <path d="M14 3H7.5A1.5 1.5 0 0 0 6 4.5v15A1.5 1.5 0 0 0 7.5 21h9a1.5 1.5 0 0 0 1.5-1.5V7z" />
  <path d="M14 3v4.5H18" />
</>);

export const IconBell = mk(<>
  <path d="M18 9.2a6 6 0 1 0-12 0c0 4.6-1.4 6-1.4 6h14.8s-1.4-1.4-1.4-6z" />
  <path d="M13.7 19.4a2 2 0 0 1-3.4 0" />
</>);

export const IconSearch = mk(<>
  <circle cx="11" cy="11" r="6.5" />
  <path d="m16 16 4 4" />
</>);

export const IconBattery = mk(<>
  <rect x="2.5" y="7.5" width="16" height="9" rx="2.2" />
  <path d="M21 10.5v3" />
</>);

export const IconAndroid = mk(<>
  <path d="M4.5 10.5h15v6.2a1.3 1.3 0 0 1-1.3 1.3H5.8a1.3 1.3 0 0 1-1.3-1.3z" />
  <path d="M7.5 10.5a4.5 4.5 0 0 1 9 0" />
  <path d="m7 5 1.3 2M17 5l-1.3 2" />
</>);

export const IconSave = mk(<>
  <path d="M5.5 4.5h10.2L19.5 8.3v11.2a1 1 0 0 1-1 1h-13a1 1 0 0 1-1-1v-14a1 1 0 0 1 1-1z" />
  <path d="M8.5 4.5v5h6v-5M8 20.5v-5.2h8v5.2" />
</>);

export const IconShield = mk(<>
  <path d="M12 3.2 5 6v5.4c0 4.2 2.8 7.7 7 9.4 4.2-1.7 7-5.2 7-9.4V6z" />
</>);

export const IconMonitor = mk(<>
  <rect x="3" y="4.5" width="18" height="12" rx="2" />
  <path d="M8.5 20.5h7M12 16.5v4" />
</>);

export const IconHome = mk(<>
  <path d="M4 10.4 12 4l8 6.4v8.1a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 18.5z" />
</>);

export const IconGrid = mk(<>
  <rect x="3.5" y="3.5" width="7" height="7" rx="1.6" />
  <rect x="13.5" y="3.5" width="7" height="7" rx="1.6" />
  <rect x="3.5" y="13.5" width="7" height="7" rx="1.6" />
  <rect x="13.5" y="13.5" width="7" height="7" rx="1.6" />
</>);

export const IconKey = mk(<>
  <circle cx="8.5" cy="12" r="4" />
  <path d="M12.5 12H21l-1.8 2.4M17 12v2.6" />
</>);

export const IconSliders = mk(<>
  <path d="M5 6.5h14M5 12h14M5 17.5h14" />
  <circle cx="9.5" cy="6.5" r="1.9" />
  <circle cx="15" cy="12" r="1.9" />
  <circle cx="8" cy="17.5" r="1.9" />
</>);

export const IconUser = mk(<>
  <circle cx="12" cy="8" r="4" />
  <path d="M4.5 20.2a7.5 7.5 0 0 1 15 0" />
</>);

export const IconUsers = mk(<>
  <circle cx="9.5" cy="8" r="3.6" />
  <path d="M3 19.8a6.6 6.6 0 0 1 13 0" />
  <path d="M16 4.8a3.6 3.6 0 0 1 0 6.9M17.6 14.4a6.6 6.6 0 0 1 3.4 5.4" />
</>);

export const IconMail = mk(<>
  <rect x="3" y="5.5" width="18" height="13" rx="2.2" />
  <path d="m3.6 7 8.4 5.6L20.4 7" />
</>);

export const IconClose = mk(<path d="m6.5 6.5 11 11m0-11-11 11" />);
