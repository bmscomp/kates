#!/usr/bin/env python3
"""
Generate a modern, high-impact PowerPoint presentation for Kates:
"Kates: The Autonomous Next-Generation Kafka Platform"
"""

import os
from pptx import Presentation
from pptx.util import Inches, Pt
from pptx.enum.text import PP_ALIGN
from pptx.enum.shapes import MSO_SHAPE
from pptx.dml.color import RGBColor

# ---------------------------------------------------------
# Presentation Constants & Color Palette (Dark High-Tech Theme)
# ---------------------------------------------------------
SLIDE_WIDTH = Inches(13.333)
SLIDE_HEIGHT = Inches(7.5)

BG_COLOR = RGBColor(10, 15, 29)         # #0A0F1D (Obsidian Navy)
CARD_BG = RGBColor(21, 29, 50)          # #151D32 (Card Slate)
CARD_BORDER = RGBColor(35, 48, 78)      # #23304E
CARD_BORDER_CYAN = RGBColor(56, 189, 248) # Cyan highlight
CARD_BORDER_RED = RGBColor(244, 63, 94)   # Rose highlight
CARD_BORDER_GREEN = RGBColor(16, 185, 129) # Emerald highlight

ACCENT_CYAN = RGBColor(56, 189, 248)    # #38BDF8
ACCENT_BLUE = RGBColor(0, 210, 255)     # #00D2FF
ACCENT_INDIGO = RGBColor(99, 102, 241)  # #6366F1
ACCENT_GREEN = RGBColor(16, 185, 129)   # #10B981
ACCENT_AMBER = RGBColor(245, 158, 11)   # #F59E0B
ACCENT_RED = RGBColor(244, 63, 94)      # #F43F5E
ACCENT_PURPLE = RGBColor(168, 85, 247)  # #A855F7

TEXT_WHITE = RGBColor(255, 255, 255)
TEXT_TITLE = RGBColor(248, 250, 252)    # #F8FAFC
TEXT_BODY = RGBColor(195, 207, 224)     # #C3CFE0
TEXT_MUTED = RGBColor(148, 163, 184)    # #94A3B8

FONT_HEADING = "Helvetica"
FONT_BODY = "Arial"

LOGO_PATH = "docs/assets/kates-logo.png"

def create_deck():
    prs = Presentation()
    prs.slide_width = SLIDE_WIDTH
    prs.slide_height = SLIDE_HEIGHT
    blank_layout = prs.slide_layouts[6]

    def set_bg(slide):
        bg = slide.shapes.add_shape(MSO_SHAPE.RECTANGLE, 0, 0, SLIDE_WIDTH, SLIDE_HEIGHT)
        bg.fill.solid()
        bg.fill.fore_color.rgb = BG_COLOR
        bg.line.color.rgb = BG_COLOR
        return bg

    def add_header(slide, badge_text, title_text, subtitle_text=None, badge_color=ACCENT_CYAN):
        # Badge Pill
        badge_box = slide.shapes.add_shape(
            MSO_SHAPE.ROUNDED_RECTANGLE,
            Inches(0.8), Inches(0.45), Inches(2.8), Inches(0.35)
        )
        badge_box.fill.solid()
        badge_box.fill.fore_color.rgb = RGBColor(20, 35, 65)
        badge_box.line.color.rgb = badge_color
        badge_box.line.width = Pt(1.2)
        tf_b = badge_box.text_frame
        tf_b.word_wrap = False
        tf_b.margin_left = tf_b.margin_top = tf_b.margin_right = tf_b.margin_bottom = 0
        p_b = tf_b.paragraphs[0]
        p_b.text = badge_text.upper()
        p_b.font.size = Pt(11)
        p_b.font.bold = True
        p_b.font.color.rgb = badge_color
        p_b.font.name = FONT_HEADING
        p_b.alignment = PP_ALIGN.CENTER

        # Title
        tb = slide.shapes.add_textbox(Inches(0.8), Inches(0.85), Inches(10.5), Inches(0.65))
        tf = tb.text_frame
        tf.word_wrap = True
        tf.margin_left = tf.margin_top = tf.margin_right = tf.margin_bottom = 0
        p = tf.paragraphs[0]
        p.text = title_text
        p.font.size = Pt(26)
        p.font.bold = True
        p.font.color.rgb = TEXT_TITLE
        p.font.name = FONT_HEADING

        # Subtitle
        if subtitle_text:
            tb_s = slide.shapes.add_textbox(Inches(0.8), Inches(1.5), Inches(11.0), Inches(0.4))
            tf_s = tb_s.text_frame
            tf_s.word_wrap = True
            tf_s.margin_left = tf_s.margin_top = tf_s.margin_right = tf_s.margin_bottom = 0
            p_s = tf_s.paragraphs[0]
            p_s.text = subtitle_text
            p_s.font.size = Pt(13)
            p_s.font.color.rgb = TEXT_MUTED
            p_s.font.name = FONT_BODY

        # Small Logo top-right
        if os.path.exists(LOGO_PATH):
            slide.shapes.add_picture(LOGO_PATH, Inches(12.0), Inches(0.45), width=Inches(0.65))

    # =========================================================================
    # SLIDE 1: Title Slide (Hero Theme)
    # =========================================================================
    s1 = prs.slides.add_slide(blank_layout)
    set_bg(s1)

    # Accent decorative bars
    accent_bar = s1.shapes.add_shape(MSO_SHAPE.RECTANGLE, Inches(0.8), Inches(1.2), Inches(0.1), Inches(4.5))
    accent_bar.fill.solid()
    accent_bar.fill.fore_color.rgb = ACCENT_BLUE
    accent_bar.line.color.rgb = ACCENT_BLUE

    # Title & Subtitle Box
    t_box = s1.shapes.add_textbox(Inches(1.2), Inches(1.2), Inches(7.5), Inches(4.5))
    tf1 = t_box.text_frame
    tf1.word_wrap = True

    p0 = tf1.paragraphs[0]
    p0.text = "NEXT-GENERATION PLATFORM ENGINEERING"
    p0.font.size = Pt(13)
    p0.font.bold = True
    p0.font.color.rgb = ACCENT_CYAN
    p0.font.name = FONT_HEADING
    p0.space_after = Pt(14)

    p1 = tf1.add_paragraph()
    p1.text = "KATES"
    p1.font.size = Pt(56)
    p1.font.bold = True
    p1.font.color.rgb = TEXT_WHITE
    p1.font.name = FONT_HEADING

    p2 = tf1.add_paragraph()
    p2.text = "The Autonomous Kafka Platform"
    p2.font.size = Pt(28)
    p2.font.bold = True
    p2.font.color.rgb = ACCENT_BLUE
    p2.font.name = FONT_HEADING
    p2.space_after = Pt(18)

    p3 = tf1.add_paragraph()
    p3.text = "From Fragile Day-1 Deployments to Long-Living, Self-Healing, Auto-Calibrated Kafka Fleets"
    p3.font.size = Pt(15)
    p3.font.color.rgb = TEXT_BODY
    p3.font.name = FONT_BODY
    p3.space_after = Pt(24)

    p4 = tf1.add_paragraph()
    p4.text = "Deploy in Seconds  •  Day-0 Calibrated  •  Continuous Self-Heal  •  Zero-Downtime Upgrades"
    p4.font.size = Pt(12)
    p4.font.bold = True
    p4.font.color.rgb = ACCENT_GREEN
    p4.font.name = FONT_HEADING

    # Hero Logo Image
    if os.path.exists(LOGO_PATH):
        s1.shapes.add_picture(LOGO_PATH, Inches(8.8), Inches(1.3), width=Inches(3.7))

    # =========================================================================
    # SLIDE 2: The Old Way vs The Kates Way (Contrast)
    # =========================================================================
    s2 = prs.slides.add_slide(blank_layout)
    set_bg(s2)
    add_header(s2, "Paradigm Shift", "The Evolution of Kafka Operations", "Moving from manual guesswork and firefighting to deterministic, autonomous engineering")

    # Left Card: The Old Way (Red/Amber Accent)
    card_old = s2.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.8), Inches(2.1), Inches(5.6), Inches(4.8))
    card_old.fill.solid()
    card_old.fill.fore_color.rgb = CARD_BG
    card_old.line.color.rgb = CARD_BORDER_RED
    card_old.line.width = Pt(1.5)
    tf_old = card_old.text_frame
    tf_old.word_wrap = True
    tf_old.margin_left = tf_old.margin_right = tf_old.margin_top = Inches(0.35)

    p = tf_old.paragraphs[0]
    p.text = "❌  THE OLD WAY: MANUAL & FRAGILE"
    p.font.size = Pt(16)
    p.font.bold = True
    p.font.color.rgb = ACCENT_RED
    p.font.name = FONT_HEADING
    p.space_after = Pt(12)

    old_points = [
        ("Blind Provisioning & Sizing:", "Engineers guess JVM heaps, buffer limits, and partition counts. No validation against underlying hardware."),
        ("Rebalancing Agony:", "Cruise-Control toil. Moving partitions floods the network, causes ISR churn, and inflates client latency."),
        ("Upgrade Anxiety:", "Upgrades are feared midnight rituals. Minor version bumps risk partition unbalance and silent cascading failures."),
        ("No Chaos Discipline:", "Disaster recovery is rarely tested. Teams discover failover bugs only during catastrophic production outages."),
        ("Configuration Rot:", "Clusters accumulate ad-hoc overrides, unmanaged topic drift, and dark debt over months of operation.")
    ]
    for title, desc in old_points:
        p = tf_old.add_paragraph()
        p.text = f"• {title} "
        p.font.bold = True
        p.font.size = Pt(11)
        p.font.color.rgb = TEXT_WHITE
        p.font.name = FONT_BODY
        run = p.add_run()
        run.text = desc
        run.font.bold = False
        run.font.color.rgb = TEXT_MUTED
        p.space_after = Pt(8)

    # Right Card: The Kates Way (Cyan/Green Accent)
    card_new = s2.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(6.8), Inches(2.1), Inches(5.7), Inches(4.8))
    card_new.fill.solid()
    card_new.fill.fore_color.rgb = CARD_BG
    card_new.line.color.rgb = CARD_BORDER_GREEN
    card_new.line.width = Pt(1.5)
    tf_new = card_new.text_frame
    tf_new.word_wrap = True
    tf_new.margin_left = tf_new.margin_right = tf_new.margin_top = Inches(0.35)

    p = tf_new.paragraphs[0]
    p.text = "⚡  THE KATES WAY: AUTONOMOUS & ROBUST"
    p.font.size = Pt(16)
    p.font.bold = True
    p.font.color.rgb = ACCENT_GREEN
    p.font.name = FONT_HEADING
    p.space_after = Pt(12)

    new_points = [
        ("Instant Provisioning & Day-0 Calibration:", "Spins up KRaft clusters in seconds; runs synthetic load sweeps to tune configs to physical IOPS."),
        ("Traffic-Governed Micro-Rebalance:", "Partitions migrate in bandwidth-throttled micro-batches with zero consumer timeouts."),
        ("Zero-Downtime Background Upgrades:", "Canary-first rollouts guarded by live synthetic probes with instant automated rollback."),
        ("Continuous Chaos & Resilience Probes:", "Faults are regularly injected during live load to mathematically prove RTO/RPO recovery."),
        ("Self-Healing Long-Living Fleet:", "Detects degraded brokers (gray failures), isolates hot spots, and expands volumes automatically.")
    ]
    for title, desc in new_points:
        p = tf_new.add_paragraph()
        p.text = f"✔ {title} "
        p.font.bold = True
        p.font.size = Pt(11)
        p.font.color.rgb = ACCENT_CYAN
        p.font.name = FONT_BODY
        run = p.add_run()
        run.text = desc
        run.font.bold = False
        run.font.color.rgb = TEXT_BODY
        p.space_after = Pt(8)

    # =========================================================================
    # SLIDE 3: What is Kates Today? (Current Foundation)
    # =========================================================================
    s3 = prs.slides.add_slide(blank_layout)
    set_bg(s3)
    add_header(s3, "Current Platform", "What is Kates Today?", "A battle-tested production toolkit combining Go CLI, Quarkus backend, and curated Kubernetes charts")

    cols = [
        {
            "badge": "PERFORMANCE ENGINE",
            "color": ACCENT_CYAN,
            "title": "8 Workload Engines",
            "items": [
                "Native virtual-thread and Trogdor backends.",
                "LOAD, STRESS, SPIKE, ENDURANCE, VOLUME, CAPACITY, ROUND_TRIP, INTEGRITY.",
                "Generates high-precision HdrHistograms (P50, P99, P99.9).",
                "Automated configuration tuning sweeps (kates tune run)."
            ]
        },
        {
            "badge": "CHAOS & RESILIENCE",
            "color": ACCENT_AMBER,
            "title": "13 Chaos Primitives",
            "items": [
                "LitmusCRD and Direct K8s providers.",
                "Broker pod kill, network partition, CPU/RAM stress, disk fill, node drain.",
                "Resilience runs: injects faults during live load to measure RTO & RPO.",
                "Automated blast-radius guardrails & rollback timeouts."
            ]
        },
        {
            "badge": "GOVERNANCE & AI AGENTS",
            "color": ACCENT_PURPLE,
            "title": "Auditing & MCP Server",
            "items": [
                "Security audits mapped to CIS, SOC 2, and PCI DSS.",
                "Automated CI/CD gate validation (kates gate).",
                "Native Model Context Protocol (MCP) server integration.",
                "Enables AI agents to safely inspect topologies and diagnose lag."
            ]
        }
    ]

    card_w = Inches(3.7)
    gap = Inches(0.3)
    start_x = Inches(0.8)

    for i, col in enumerate(cols):
        x = start_x + i * (card_w + gap)
        card = s3.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x, Inches(2.1), card_w, Inches(4.8))
        card.fill.solid()
        card.fill.fore_color.rgb = CARD_BG
        card.line.color.rgb = col["color"]
        card.line.width = Pt(1.4)
        tf = card.text_frame
        tf.word_wrap = True
        tf.margin_left = tf.margin_right = tf.margin_top = Inches(0.3)

        p = tf.paragraphs[0]
        p.text = col["badge"]
        p.font.size = Pt(10)
        p.font.bold = True
        p.font.color.rgb = col["color"]
        p.font.name = FONT_HEADING
        p.space_after = Pt(6)

        p = tf.add_paragraph()
        p.text = col["title"]
        p.font.size = Pt(18)
        p.font.bold = True
        p.font.color.rgb = TEXT_WHITE
        p.font.name = FONT_HEADING
        p.space_after = Pt(14)

        for item in col["items"]:
            p = tf.add_paragraph()
            p.text = f"• {item}"
            p.font.size = Pt(11)
            p.font.color.rgb = TEXT_BODY
            p.font.name = FONT_BODY
            p.space_after = Pt(10)

    # =========================================================================
    # SLIDE 4: The Core Paradigm: The Autonomous OODA Loop
    # =========================================================================
    s4 = prs.slides.add_slide(blank_layout)
    set_bg(s4)
    add_header(s4, "Autonomous Architecture", "The Continuous Control Loop (OODA)", "Kates replaces static reconciliation with closed-loop empirical telemetry and safe execution")

    ooda_steps = [
        ("1. OBSERVE", ACCENT_CYAN, "High-Res Telemetry", [
            "HdrHistogram latency curves",
            "Broker fsync & disk IOPS",
            "Under-replicated partitions",
            "Consumer group lag acceleration"
        ]),
        ("2. ORIENT", ACCENT_INDIGO, "Empirical Analysis", [
            "Compare to Day-0 baseline",
            "Distinguish gray failure vs load",
            "Check durability & RPO limits",
            "Correlate cluster alerts"
        ]),
        ("3. DECIDE", ACCENT_AMBER, "Policy & Planning", [
            "Trigger micro-rebalancing",
            "Quarantine degraded broker",
            "Schedule canary upgrade wave",
            "Calculate safe bandwidth throttle"
        ]),
        ("4. ACT", ACCENT_GREEN, "Surgical Execution", [
            "Micro-batch replica migrations",
            "Dynamic PVC disk expansion",
            "Demote broker leadership",
            "Auto-rollback on latency spike"
        ]),
    ]

    box_w = Inches(2.7)
    box_gap = Inches(0.3)
    x0 = Inches(0.8)

    for i, (step_title, color, sub, bullets) in enumerate(ooda_steps):
        bx = x0 + i * (box_w + box_gap)
        box = s4.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, bx, Inches(2.2), box_w, Inches(4.6))
        box.fill.solid()
        box.fill.fore_color.rgb = CARD_BG
        box.line.color.rgb = color
        box.line.width = Pt(1.6)

        tf = box.text_frame
        tf.word_wrap = True
        tf.margin_left = tf.margin_right = tf.margin_top = Inches(0.25)

        p = tf.paragraphs[0]
        p.text = step_title
        p.font.size = Pt(16)
        p.font.bold = True
        p.font.color.rgb = color
        p.font.name = FONT_HEADING

        p = tf.add_paragraph()
        p.text = sub
        p.font.size = Pt(11)
        p.font.bold = True
        p.font.color.rgb = TEXT_MUTED
        p.font.name = FONT_BODY
        p.space_after = Pt(14)

        for b in bullets:
            p = tf.add_paragraph()
            p.text = f"→ {b}"
            p.font.size = Pt(10.5)
            p.font.color.rgb = TEXT_BODY
            p.font.name = FONT_BODY
            p.space_after = Pt(8)

    # =========================================================================
    # SLIDE 5: Pillar 1: Cluster in Seconds & Day-0 Auto-Calibration
    # =========================================================================
    s5 = prs.slides.add_slide(blank_layout)
    set_bg(s5)
    add_header(s5, "Pillar 1", "Cluster in Seconds & Day-0 Auto-Calibration", "Don't guess settings. Instantiate instantly and tune configurations against physical hardware limits.")

    # Left: Instant Cluster
    c1 = s5.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.8), Inches(2.1), Inches(5.6), Inches(4.8))
    c1.fill.solid()
    c1.fill.fore_color.rgb = CARD_BG
    c1.line.color.rgb = ACCENT_BLUE
    c1.line.width = Pt(1.5)
    tf1 = c1.text_frame
    tf1.word_wrap = True
    tf1.margin_left = tf1.margin_right = tf1.margin_top = Inches(0.35)

    p = tf1.paragraphs[0]
    p.text = "⚡ INSTANT BOOTSTRAP (SECONDS)"
    p.font.size = Pt(16)
    p.font.bold = True
    p.font.color.rgb = ACCENT_BLUE
    p.font.name = FONT_HEADING
    p.space_after = Pt(14)

    items1 = [
        ("Native KRaft Architecture:", "Pure metadata quorum with zero ZooKeeper operational overhead."),
        ("Profile-Driven Presets:", "Pre-tuned templates: 'high-throughput', 'low-latency-financial', 'high-durability-multi-az'."),
        ("Multi-Zone Rack Awareness:", "Automatically maps broker pools to Kubernetes node availability zones (alpha, sigma, gamma)."),
        ("Single-Command Provisioning:", "One CLI command spins up complete cluster, Prometheus monitoring, and security baselines.")
    ]
    for h, b in items1:
        p = tf1.add_paragraph()
        p.text = f"• {h} "
        p.font.bold = True
        p.font.size = Pt(11)
        p.font.color.rgb = TEXT_WHITE
        p.font.name = FONT_BODY
        r = p.add_run()
        r.text = b
        r.font.bold = False
        r.font.color.rgb = TEXT_MUTED
        p.space_after = Pt(10)

    # Right: Day-0 Calibration
    c2 = s5.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(6.8), Inches(2.1), Inches(5.7), Inches(4.8))
    c2.fill.solid()
    c2.fill.fore_color.rgb = CARD_BG
    c2.line.color.rgb = ACCENT_GREEN
    c2.line.width = Pt(1.5)
    tf2 = c2.text_frame
    tf2.word_wrap = True
    tf2.margin_left = tf2.margin_right = tf2.margin_top = Inches(0.35)

    p = tf2.paragraphs[0]
    p.text = "🎯 DAY-0 EMPIRICAL CALIBRATION"
    p.font.size = Pt(16)
    p.font.bold = True
    p.font.color.rgb = ACCENT_GREEN
    p.font.name = FONT_HEADING
    p.space_after = Pt(14)

    items2 = [
        ("Synthetic Hardware Probes:", "Measures actual sequential disk write IOPS, fsync latency, and network card bandwidth limits."),
        ("Automated Parameter Tuning:", "Calculates optimal log.flush.interval, socket buffer sizes, and thread pools based on empirical facts."),
        ("The 'Golden Baseline':", "Locks an immutable fingerprint of the hardware limits. Future regressions are caught instantly."),
        ("No Production Surprises:", "Validates cluster capabilities BEFORE production traffic touches the brokers.")
    ]
    for h, b in items2:
        p = tf2.add_paragraph()
        p.text = f"✔ {h} "
        p.font.bold = True
        p.font.size = Pt(11)
        p.font.color.rgb = ACCENT_GREEN
        p.font.name = FONT_BODY
        r = p.add_run()
        r.text = b
        r.font.bold = False
        r.font.color.rgb = TEXT_BODY
        p.space_after = Pt(10)

    # =========================================================================
    # SLIDE 6: Pillar 2: Autonomous Scaling & Micro-Rebalancing
    # =========================================================================
    s6 = prs.slides.add_slide(blank_layout)
    set_bg(s6)
    add_header(s6, "Pillar 2", "Autonomous Scaling & Micro-Rebalancing", "Eliminating Cruise-Control toil through load-governed micro-migrations and zero-hotspot guarantees")

    features_s6 = [
        ("Predictive Elastic Expansion", ACCENT_CYAN, [
            "Monitors storage consumption curves and partition skew trends.",
            "Automatically scales KafkaNodePool replicas before disk watermarks trigger panic.",
            "Zero human ticket creation needed for broker provisioning."
        ]),
        ("Bandwidth-Governed Micro-Rebalance", ACCENT_INDIGO, [
            "Replaces massive, cluster-saturating reassignments with paced micro-steps.",
            "Dynamically throttles replication bandwidth based on real-time client P99 latency.",
            "Zero client timeouts or dropped connections during data movement."
        ]),
        ("Hot-Spot & Celebrity Partition Isolation", ACCENT_PURPLE, [
            "Continuously measures per-partition I/O and consumer lag skew.",
            "Automatically isolates ultra-high throughput partitions onto dedicated brokers.",
            "Maintains perfectly balanced leader distribution across nodes."
        ])
    ]

    for i, (title, color, points) in enumerate(features_s6):
        x = start_x + i * (card_w + gap)
        card = s6.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x, Inches(2.1), card_w, Inches(4.8))
        card.fill.solid()
        card.fill.fore_color.rgb = CARD_BG
        card.line.color.rgb = color
        card.line.width = Pt(1.5)
        tf = card.text_frame
        tf.word_wrap = True
        tf.margin_left = tf.margin_right = tf.margin_top = Inches(0.3)

        p = tf.paragraphs[0]
        p.text = title
        p.font.size = Pt(16)
        p.font.bold = True
        p.font.color.rgb = color
        p.font.name = FONT_HEADING
        p.space_after = Pt(14)

        for pt in points:
            p = tf.add_paragraph()
            p.text = f"• {pt}"
            p.font.size = Pt(11)
            p.font.color.rgb = TEXT_BODY
            p.font.name = FONT_BODY
            p.space_after = Pt(12)

    # =========================================================================
    # SLIDE 7: Pillar 3: Continuous Self-Healing & Gray Failure Quarantine
    # =========================================================================
    s7 = prs.slides.add_slide(blank_layout)
    set_bg(s7)
    add_header(s7, "Pillar 3", "Continuous Self-Healing & Gray Failure Quarantine", "Brokers are cattle, not pets. Detect degraded hardware and heal partition health autonomously.")

    # Card 1: Slow Broker Quarantine
    c_sh1 = s7.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.8), Inches(2.1), Inches(3.7), Inches(4.8))
    c_sh1.fill.solid()
    c_sh1.fill.fore_color.rgb = CARD_BG
    c_sh1.line.color.rgb = ACCENT_AMBER
    c_sh1.line.width = Pt(1.4)
    tf1 = c_sh1.text_frame
    tf1.word_wrap = True
    tf1.margin_left = tf1.margin_right = tf1.margin_top = Inches(0.3)

    p = tf1.paragraphs[0]
    p.text = "GRAY FAILURE ISOLATION"
    p.font.size = Pt(15)
    p.font.bold = True
    p.font.color.rgb = ACCENT_AMBER
    p.font.name = FONT_HEADING
    p.space_after = Pt(12)

    sh1_pts = [
        "Dead nodes are easy; degraded nodes (gray failures) take clusters down.",
        "Detects slow NVMe/EBS disks via fsync outlier metrics.",
        "Surgically demotes partition leaders away from degraded brokers.",
        "Quarantines node from active traffic while maintaining replica sync."
    ]
    for pt in sh1_pts:
        p = tf1.add_paragraph()
        p.text = f"→ {pt}"
        p.font.size = Pt(10.5)
        p.font.color.rgb = TEXT_BODY
        p.font.name = FONT_BODY
        p.space_after = Pt(10)

    # Card 2: Auto PVC Disk Expansion
    c_sh2 = s7.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(4.8), Inches(2.1), Inches(3.7), Inches(4.8))
    c_sh2.fill.solid()
    c_sh2.fill.fore_color.rgb = CARD_BG
    c_sh2.line.color.rgb = ACCENT_GREEN
    c_sh2.line.width = Pt(1.4)
    tf2 = c_sh2.text_frame
    tf2.word_wrap = True
    tf2.margin_left = tf2.margin_right = tf2.margin_top = Inches(0.3)

    p = tf2.paragraphs[0]
    p.text = "AUTOMATED PVC EXPANSION"
    p.font.size = Pt(15)
    p.font.bold = True
    p.font.color.rgb = ACCENT_GREEN
    p.font.name = FONT_HEADING
    p.space_after = Pt(12)

    sh2_pts = [
        "Monitors disk fill velocity and volume watermarks in real-time.",
        "Prevents DiskFull database corruptions before they occur.",
        "Dynamically triggers Kubernetes StorageClass volume expansion on the fly.",
        "Adjusts topic retention limits as safety valve during runaway spikes."
    ]
    for pt in sh2_pts:
        p = tf2.add_paragraph()
        p.text = f"→ {pt}"
        p.font.size = Pt(10.5)
        p.font.color.rgb = TEXT_BODY
        p.font.name = FONT_BODY
        p.space_after = Pt(10)

    # Card 3: Zombie & Controller Healing
    c_sh3 = s7.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(8.8), Inches(2.1), Inches(3.7), Inches(4.8))
    c_sh3.fill.solid()
    c_sh3.fill.fore_color.rgb = CARD_BG
    c_sh3.line.color.rgb = ACCENT_CYAN
    c_sh3.line.width = Pt(1.4)
    tf3 = c_sh3.text_frame
    tf3.word_wrap = True
    tf3.margin_left = tf3.margin_right = tf3.margin_top = Inches(0.3)

    p = tf3.paragraphs[0]
    p.text = "ZOMBIE & REPLICA HEALING"
    p.font.size = Pt(15)
    p.font.bold = True
    p.font.color.rgb = ACCENT_CYAN
    p.font.name = FONT_HEADING
    p.space_after = Pt(12)

    sh3_pts = [
        "Detects hung replica fetchers and frozen KRaft metadata controllers.",
        "Issues non-disruptive, targeted thread dumps and pod restarts.",
        "Reclaims orphaned concurrency slots and cleans stranded test runs.",
        "Continuous self-audit ensures cluster never drifts into dark state."
    ]
    for pt in sh3_pts:
        p = tf3.add_paragraph()
        p.text = f"→ {pt}"
        p.font.size = Pt(10.5)
        p.font.color.rgb = TEXT_BODY
        p.font.name = FONT_BODY
        p.space_after = Pt(10)

    # =========================================================================
    # SLIDE 8: Pillar 4: Zero-Downtime Background Canary Upgrades
    # =========================================================================
    s8 = prs.slides.add_slide(blank_layout)
    set_bg(s8)
    add_header(s8, "Pillar 4", "Zero-Downtime Background Canary Upgrades", "Upgrades with mathematical safety guarantees: continuous synthetic probes and automated instant rollback")

    # Pipeline cards across the top
    steps_up = [
        ("1. PREFLIGHT", "Verify cluster & consumer group health before touching brokers"),
        ("2. CANARY", "Roll single broker in isolated pool; observe GC & buffers for 15m"),
        ("3. ACTIVE PROBE", "Continuous synthetic produce/consume loops prove data plane SLA"),
        ("4. SAFE ROLL", "Progressive wave rollout across AZs with auto-rollback on error")
    ]
    for i, (title, sub) in enumerate(steps_up):
        bx = x0 + i * (box_w + box_gap)
        box = s8.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, bx, Inches(2.1), box_w, Inches(1.8))
        box.fill.solid()
        box.fill.fore_color.rgb = CARD_BG
        box.line.color.rgb = ACCENT_CYAN
        box.line.width = Pt(1.2)
        tf = box.text_frame
        tf.word_wrap = True
        tf.margin_left = tf.margin_right = tf.margin_top = Inches(0.2)
        p = tf.paragraphs[0]
        p.text = title
        p.font.size = Pt(13)
        p.font.bold = True
        p.font.color.rgb = ACCENT_CYAN
        p.font.name = FONT_HEADING
        p.space_after = Pt(6)
        p = tf.add_paragraph()
        p.text = sub
        p.font.size = Pt(10)
        p.font.color.rgb = TEXT_BODY
        p.font.name = FONT_BODY

    # Bottom Details: 2 Big Cards
    c_b1 = s8.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.8), Inches(4.2), Inches(5.6), Inches(2.7))
    c_b1.fill.solid()
    c_b1.fill.fore_color.rgb = CARD_BG
    c_b1.line.color.rgb = ACCENT_RED
    c_b1.line.width = Pt(1.4)
    tf1 = c_b1.text_frame
    tf1.word_wrap = True
    tf1.margin_left = tf1.margin_right = tf1.margin_top = Inches(0.25)

    p = tf1.paragraphs[0]
    p.text = "🛡️ INSTANT AUTOMATED ROLLBACK"
    p.font.size = Pt(14)
    p.font.bold = True
    p.font.color.rgb = ACCENT_RED
    p.font.name = FONT_HEADING
    p.space_after = Pt(8)

    r_items = [
        "If client error rate rises by > 0.1%, rollout aborts immediately.",
        "If P99 latency degrades past Day-0 baseline, cluster reverts automatically.",
        "Zero data loss, zero human panic, zero midnight on-call fire drills."
    ]
    for it in r_items:
        p = tf1.add_paragraph()
        p.text = f"• {it}"
        p.font.size = Pt(10.5)
        p.font.color.rgb = TEXT_BODY
        p.space_after = Pt(4)

    c_b2 = s8.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(6.8), Inches(4.2), Inches(5.7), Inches(2.7))
    c_b2.fill.solid()
    c_b2.fill.fore_color.rgb = CARD_BG
    c_b2.line.color.rgb = ACCENT_GREEN
    c_b2.line.width = Pt(1.4)
    tf2 = c_b2.text_frame
    tf2.word_wrap = True
    tf2.margin_left = tf2.margin_right = tf2.margin_top = Inches(0.25)

    p = tf2.paragraphs[0]
    p.text = "🔄 SCHEMA & CRD HARMONIZATION"
    p.font.size = Pt(14)
    p.font.bold = True
    p.font.color.rgb = ACCENT_GREEN
    p.font.name = FONT_HEADING
    p.space_after = Pt(8)

    h_items = [
        "Coordinates Strimzi CRD upgrades with proxy & custom CA bundle support.",
        "Sequences Kafka protocol (inter.broker.protocol.version) smoothly.",
        "Guarantees that client drivers never encounter unsupported protocol frames."
    ]
    for it in h_items:
        p = tf2.add_paragraph()
        p.text = f"• {it}"
        p.font.size = Pt(10.5)
        p.font.color.rgb = TEXT_BODY
        p.space_after = Pt(4)

    # =========================================================================
    # SLIDE 9: Comprehensive Capability Comparison (Matrix Table)
    # =========================================================================
    s9 = prs.slides.add_slide(blank_layout)
    set_bg(s9)
    add_header(s9, "Feature Matrix", "How Kates Compares", "Side-by-side comparison across deployment, resilience, scaling, and lifecycle management")

    # Table
    rows = 7
    cols_n = 4
    table_shape = s9.shapes.add_table(rows, cols_n, Inches(0.8), Inches(2.1), Inches(11.7), Inches(4.7))
    table = table_shape.table
    table.columns[0].width = Inches(2.5)
    table.columns[1].width = Inches(3.0)
    table.columns[2].width = Inches(3.0)
    table.columns[3].width = Inches(3.2)

    headers = ["Capability", "Old-School Bare Metal / VM", "Basic K8s Operator", "Kates Autonomous Operator"]
    for c_idx, h in enumerate(headers):
        cell = table.cell(0, c_idx)
        cell.fill.solid()
        cell.fill.fore_color.rgb = RGBColor(26, 38, 66)
        p = cell.text_frame.paragraphs[0]
        p.text = h
        p.font.bold = True
        p.font.size = Pt(11)
        p.font.color.rgb = ACCENT_CYAN if c_idx == 3 else TEXT_WHITE
        p.alignment = PP_ALIGN.CENTER if c_idx > 0 else PP_ALIGN.LEFT

    matrix_data = [
        ("Deployment Time", "Days to Weeks (Manual)", "Minutes (YAMLs)", "Seconds (Instant Profiles)"),
        ("Hardware Sizing", "Human Guesswork", "Static YAML limits", "Day-0 Empirical Calibration"),
        ("Benchmarking", "Ad-hoc scripts / absent", "Separate manual tools", "Native 8-Engine Verification"),
        ("Chaos / Resilience", "Untested or dangerous", "Untested in staging", "Continuous, safe fault injection"),
        ("Partition Rebalance", "Cruise-Control toil", "Manual CRD triggers", "Autonomous, latency-throttled"),
        ("Upgrades & Rollback", "High-anxiety midnight fire drills", "Rolling restarts blind to SLA", "Canary-proved, instant auto-rollback")
    ]

    for r_idx, row in enumerate(matrix_data, start=1):
        for c_idx, val in enumerate(row):
            cell = table.cell(r_idx, c_idx)
            cell.fill.solid()
            # alternating row background
            cell.fill.fore_color.rgb = RGBColor(16, 23, 40) if r_idx % 2 == 0 else CARD_BG
            p = cell.text_frame.paragraphs[0]
            p.text = val
            p.font.size = Pt(10)
            p.font.name = FONT_BODY
            if c_idx == 0:
                p.font.bold = True
                p.font.color.rgb = TEXT_WHITE
            elif c_idx == 3:
                p.font.bold = True
                p.font.color.rgb = ACCENT_GREEN
                p.alignment = PP_ALIGN.CENTER
            else:
                p.font.color.rgb = TEXT_MUTED
                p.alignment = PP_ALIGN.CENTER

    # =========================================================================
    # SLIDE 10: Business Value & Return on Investment (ROI)
    # =========================================================================
    s10 = prs.slides.add_slide(blank_layout)
    set_bg(s10)
    add_header(s10, "Business Impact", "Quantified Platform Value & ROI", "Transforming Kafka from an operational cost center into a resilient, self-funding competitive asset")

    roi_cards = [
        ("90%", "Less On-Call Toil", ACCENT_CYAN, "Autonomous healing resolves gray failures, volume exhaustion, and partition hot-spots before engineers are paged."),
        ("3x", "Infrastructure Efficiency", ACCENT_GREEN, "Stop overprovisioning by 300%. Day-0 calibration and empirical scaling optimize exact hardware saturation."),
        ("100%", "Contractual SLA Proof", ACCENT_BLUE, "Back developer SLAs with empirical resilience grades and continuous chaos verification."),
        ("AI-Ready", "Autonomous Fleet Agent", ACCENT_PURPLE, "Native Model Context Protocol (MCP) server allows AI agents to monitor and troubleshoot Kafka safely.")
    ]

    for i, (stat, title, color, desc) in enumerate(roi_cards):
        bx = x0 + i * (box_w + box_gap)
        box = s10.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, bx, Inches(2.2), box_w, Inches(4.6))
        box.fill.solid()
        box.fill.fore_color.rgb = CARD_BG
        box.line.color.rgb = color
        box.line.width = Pt(1.5)

        tf = box.text_frame
        tf.word_wrap = True
        tf.margin_left = tf.margin_right = tf.margin_top = Inches(0.25)

        p = tf.paragraphs[0]
        p.text = stat
        p.font.size = Pt(40)
        p.font.bold = True
        p.font.color.rgb = color
        p.font.name = FONT_HEADING
        p.space_after = Pt(2)

        p = tf.add_paragraph()
        p.text = title
        p.font.size = Pt(14)
        p.font.bold = True
        p.font.color.rgb = TEXT_WHITE
        p.font.name = FONT_HEADING
        p.space_after = Pt(14)

        p = tf.add_paragraph()
        p.text = desc
        p.font.size = Pt(11)
        p.font.color.rgb = TEXT_BODY
        p.font.name = FONT_BODY

    # =========================================================================
    # SLIDE 11: The Strategic Roadmap & Vision
    # =========================================================================
    s11 = prs.slides.add_slide(blank_layout)
    set_bg(s11)
    add_header(s11, "The Future Ahead", "The Strategic Roadmap to Fleet Autonomy", "Executing the transition from foundational verification tools to autonomous fleet orchestration")

    phases = [
        ("PHASE 1: CURRENT (v1.25)", ACCENT_CYAN, [
            "8 Workload Benchmark Engines",
            "13 Chaos Primitives with Safety Guardrails",
            "CIS & Security Configuration Auditing",
            "Model Context Protocol (MCP) Agent Interface"
        ]),
        ("PHASE 2: NEAR-TERM (v1.26 - v1.28)", ACCENT_BLUE, [
            "Multi-Cluster Federation & Fleet Comparison",
            "Persistent Latency HdrHistograms",
            "Automated Day-0 Calibration Engine",
            "Nonparametric Statistical Significance Gates"
        ]),
        ("PHASE 3: LONG-TERM (v2.0 AUTONOMOUS)", ACCENT_GREEN, [
            "Autonomous Closed-Loop Control Plane",
            "Dynamic Bandwidth-Governed Micro-Rebalance",
            "Zero-Downtime Canary Upgrades with Auto-Rollback",
            "Self-Healing Long-Living Multi-Cloud Fleets"
        ])
    ]

    for i, (phase_title, color, bullets) in enumerate(phases):
        x = start_x + i * (card_w + gap)
        card = s11.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, x, Inches(2.1), card_w, Inches(3.6))
        card.fill.solid()
        card.fill.fore_color.rgb = CARD_BG
        card.line.color.rgb = color
        card.line.width = Pt(1.5)
        tf = card.text_frame
        tf.word_wrap = True
        tf.margin_left = tf.margin_right = tf.margin_top = Inches(0.25)

        p = tf.paragraphs[0]
        p.text = phase_title
        p.font.size = Pt(13)
        p.font.bold = True
        p.font.color.rgb = color
        p.font.name = FONT_HEADING
        p.space_after = Pt(10)

        for b in bullets:
            p = tf.add_paragraph()
            p.text = f"✔ {b}"
            p.font.size = Pt(10.5)
            p.font.color.rgb = TEXT_BODY
            p.font.name = FONT_BODY
            p.space_after = Pt(8)

    # Bottom Quote Box
    q_box = s11.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(0.8), Inches(6.0), Inches(11.7), Inches(0.95))
    q_box.fill.solid()
    q_box.fill.fore_color.rgb = RGBColor(16, 28, 52)
    q_box.line.color.rgb = ACCENT_CYAN
    q_box.line.width = Pt(1.2)
    tf_q = q_box.text_frame
    tf_q.word_wrap = True
    tf_q.margin_left = tf_q.margin_right = tf_q.margin_top = Inches(0.15)
    p = tf_q.paragraphs[0]
    p.text = "“Infrastructure software should not require human life support. Kates turns Apache Kafka from a fragile operational burden into an autonomous, self-calibrating, and self-healing foundation for the modern enterprise.”"
    p.font.size = Pt(11.5)
    p.font.italic = True
    p.font.color.rgb = TEXT_WHITE
    p.font.name = FONT_BODY
    p.alignment = PP_ALIGN.CENTER

    output_file = "kates-autonomous-platform.pptx"
    prs.save(output_file)
    print(f"Presentation saved successfully to {output_file}")

if __name__ == "__main__":
    create_deck()
