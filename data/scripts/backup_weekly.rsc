# backup script

:local date [/system clock get date];
:local time [/system clock get time];

:local year [:pick $date 7 11];
:local day [:pick $date 4 6];
:local monthText [:pick $date 0 3];
:local monthsList ("jan","feb","mar","apr","may","jun","jul","aug","sep","oct","nov","dec");
:local month ([ :find $monthsList $monthText -1 ] + 1);
:if ($month < 10) do={ :set month ("0" . $month); }

:local hour [:pick $time 0 2];
:local min [:pick $time 3 5];
:local sec [:pick $time 6 8];

:local filename ("backup/".$year.".".$month.".".$day."-".$hour.$min.$sec);

/system backup save name=$filename;
