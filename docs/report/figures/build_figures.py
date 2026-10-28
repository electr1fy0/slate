"""Rebuild report figures from the included measurements and explicit models."""
from pathlib import Path
from datetime import datetime, timezone
import csv
import json
import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "figures"
D = json.loads((ROOT / "data/cloud-measurements.json").read_text())
plt.rcParams.update({
    "font.family": "serif", "font.serif": ["STIXGeneral"],
    "mathtext.fontset": "stix", "font.size": 11,
    "axes.labelsize": 11, "axes.titlesize": 12,
    "axes.spines.top": False, "axes.spines.right": False,
    "axes.linewidth": .7, "grid.color": ".86", "grid.linewidth": .6,
    "savefig.bbox": "tight", "pdf.fonttype": 42,
})

def save(fig, name):
    fig.savefig(OUT / (name + ".pdf"))
    fig.savefig(OUT / (name + ".png"), dpi=180)
    plt.close(fig)

def seconds(stamp):
    return datetime.fromisoformat(stamp.replace("Z", "+00:00")).timestamp()

run = D["rehearsal"]
origin = run["fault_injected_at_ms"] / 1000
events = run["events"]
pick = lambda kind: next(e for e in events if e["type"] == kind)
workflow = seconds(pick("workflow")["at"]) - origin
promotion = seconds(pick("failover")["at"]) - origin
smoke = seconds(pick("recovered")["at"]) - origin
reconcile = seconds(pick("reconcile")["at"]) - origin
external = run["fault_to_observed_recovery_ms"] / 1000
controller = D["inspection"]["executions"][0]["public_smoke"]["duration_ms"] / 1000
check_start = smoke - controller

# Recorded event times and the two distinct measurement windows.
fig, axes = plt.subplots(2, 1, figsize=(7.4, 5.5), sharex=True,
                         gridspec_kw={"height_ratios": [3.5, 1.7]}, layout="constrained")
ax = axes[0]
names = ["Fault action starts", "Workflow-start event", "B promoted, epoch 2",
         "Public route verified", "Audit reconciliation recorded", "Recovery observed externally"]
times = [0, workflow, promotion, smoke, reconcile, external]
ys = np.arange(len(times))[::-1]
for y, t in zip(ys, times):
    ax.plot([0, t], [y, y], color=".72", lw=1)
    ax.plot(t, y, "o", color=".12", ms=5)
    ax.text(t + .9, y, f"{t:.3f} s", va="center", fontsize=10)
ax.set_yticks(ys, names)
ax.set_ylim(-.7, len(times)-.3)
ax.set_xlim(-1, 61)
ax.grid(axis="x")
ax.set_title("Recorded AWS event sequence · 10 October 2026", loc="left", pad=12)
ax.tick_params(axis="y", length=0)
axes[1].broken_barh([(0, external)], (1.5, .5), facecolors=".25")
axes[1].broken_barh([(check_start, controller)], (.5, .5), facecolors=".67", edgecolors=".2", hatch="///")
axes[1].set_yticks([1.75, .75], ["External observation", "Controller window"])
axes[1].set_ylim(0, 2.5)
axes[1].set_xlabel("Seconds from the start of the fault action")
axes[1].grid(axis="x")
axes[1].text(external + .9, 1.75, "50.472 s", va="center", fontsize=10)
axes[1].text(check_start + controller/2, .75, "23.713 s", ha="center", va="center", fontsize=10)
save(fig, "cloud-timeline")
with (ROOT / "data/cloud-timeline.csv").open("w") as f:
    w=csv.writer(f); w.writerow(["recorded_event", "seconds_from_fault_action"])
    w.writerows(zip(names,times))

# Writer authority is a step change; process restart is not an authority grant.
fig, ax=plt.subplots(figsize=(7.1, 3.1), layout="constrained")
ax.step([0,promotion,external],[1,2,2],where="post",color=".12",lw=2)
ax.scatter([promotion],[2],s=36,color=".12",zorder=5)
ax.axvline(promotion,color=".45",ls="--",lw=.8)
ax.text(12,.99,"A owns epoch 1",ha="center",va="top")
ax.text(48.5,2.04,"B owns epoch 2",ha="center",va="bottom")
ax.annotate("Old A/1 writes fail the\ntransactional writer condition",xy=(48,1.5),xytext=(11,1.52),
            arrowprops={"arrowstyle":"->","color":".3"},va="center",fontsize=10)
ax.set(xlim=(0,56),ylim=(.7,2.35),yticks=[1,2],ylabel="Authoritative writer epoch",
       xlabel="Seconds from the start of the fault action")
ax.grid(axis="y")
save(fig,"writer-epoch")

# A scan count is table work, not the number of pending submissions.
executions=D["inspection"]["executions"]
labels=["Automatic recovery", "Repeated reconciliation"]
scanned=[e["summary"]["scanned"] for e in executions]
processed=[e["summary"]["processed"] for e in executions]
fig,axes=plt.subplots(1,2,figsize=(7.2,3.5),layout="constrained")
for ax,vals,title,ymax in zip(axes,[scanned,processed],["Table items scanned","New audit work processed"],[38,1.4]):
    bars=ax.bar([0,1],vals,color=[".25",".72"],edgecolor=".15",width=.55)
    bars[1].set_hatch("///")
    ax.set_xticks([0,1],["Automatic", "Repeated"])
    ax.set_title(title,loc="left",pad=13)
    ax.set_ylim(0,ymax)
    ax.grid(axis="y");ax.set_axisbelow(True)
    for i,val in enumerate(vals):ax.text(i,val+ymax*.035,str(val),ha="center",va="bottom")
axes[0].set_ylabel("Count")
axes[1].set_yticks([0,1])
fig.suptitle("Both executions completed with zero reconciliation conflicts",fontsize=11)
save(fig,"reconciliation-work")

# Analytical scenarios: fixed schedule, three probes, five-second spacing.
phase=np.linspace(0,1,101)
fig,ax=plt.subplots(figsize=(7.1,3.8),layout="constrained")
for schedule,style,c in [(60,"-",".1"),(120,"--",".4"),(300,":",".6")]:
    nominal=schedule*(1-phase)+10
    ax.plot(phase*100,nominal,ls=style,c=c,lw=1.8,label=f"{schedule//60}-minute schedule")
ax.set(xlabel="Position of a fault within the schedule cycle (%)",
       ylabel="Nominal delay to completed confirmation (s)",xlim=(0,100),ylim=(0,325))
ax.grid();ax.legend(frameon=False)
ax.set_title("Analytical model: three probes with two 5-second gaps",loc="left",pad=12)
save(fig,"monitor-phase-model")

hours=np.linspace(0,24,97)
fig,ax=plt.subplots(figsize=(7.1,3.8),layout="constrained")
for schedule,style,c in [(60,"-",".1"),(120,"--",".4"),(300,":",".6")]:
    ax.plot(hours,3600*hours/schedule,ls=style,c=c,lw=1.8,label=f"{schedule//60}-minute schedule")
ax.scatter([2],[120],color=".1",s=28,zorder=3)
ax.annotate("2-hour rehearsal: 120 nominal triggers\nat the implemented 1-minute rate",xy=(2,120),xytext=(3,1030),
            arrowprops={"arrowstyle":"->","color":".3"},fontsize=10,va="top",
            bbox={"facecolor":"white","edgecolor":"none","pad":3})
ax.set(xlabel="Time monitoring is enabled (hours)",ylabel="Nominal scheduled monitor invocations",xlim=(0,24),ylim=(0,1550))
ax.grid();ax.legend(frameon=False,loc="upper left")
ax.set_title("Schedule activity model; retries and manual runs excluded",loc="left",pad=12)
save(fig,"monitor-activity-model")
with (ROOT/"data/model-parameters.json").open("w") as f:
    json.dump({"kind":"analytical model, not measured performance", "schedule_seconds":[60,120,300],
               "confirmation_probes":3,"probe_spacing_seconds":5,
               "delay_formula":"S*(1-phase)+(p-1)*delta", "activity_formula":"3600*h/S",
               "excluded":"probe duration, schedule jitter, Lambda/workflow delay, retries, manual invocations"},f,indent=2)
print("Built five vector plots with source data and explicit model parameters.")
