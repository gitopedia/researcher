Set-Location C:\Solus\Gitopedia\researcher

$prompts = @{
    "016" = "Bauhaus design interpretation of atomic structure, primary colors, geometric shapes, functional minimalism"
    "017" = "Psychedelic 1960s poster style quantum visualization, swirling colors, kaleidoscopic patterns, fractal elements"
    "018" = "Brutalist graphic design of quantum mechanics, heavy typography, raw textures, stark contrasts, industrial feel"
    "019" = "Art nouveau flowing curves depicting electron orbitals, organic forms, decorative borders, muted earth tones"
    "020" = "Digital glitch art of quantum uncertainty, corrupted pixels, RGB splitting, data moshing effects"
    "021" = "Quantum waves in monochromatic blue palette, from navy to ice blue, depth through value only"
    "022" = "Quantum particles glowing in warm sunset colors, oranges reds and golds, ember-like energy"
    "023" = "Radioactive-looking quantum field in acid green and black, toxic glow, hazard aesthetic"
    "024" = "Soft pastel visualization of superposition, baby pink and mint, gentle and approachable"
    "025" = "Extreme high contrast black and white quantum imagery, stark shadows, no midtones"
    "026" = "Duotone image in purple and gold of entangled particles, elegant and regal"
    "027" = "Full neon rainbow spectrum quantum energy burst, vibrant colors, celebratory"
    "028" = "Quantum physics in natural earth tones, browns greens and terracotta, organic science"
    "029" = "Frozen quantum state in ice blue and white, crystalline structures, absolute zero feeling"
    "030" = "Quantum energy as pure fire, reds oranges yellows, plasma-like intensity"
    "031" = "Pure grayscale quantum mechanics visualization, no color, focus on form and light"
    "032" = "Retrowave color scheme for quantum concepts, hot pink magenta and cyan on dark purple"
    "033" = "Vintage muted colors depicting wave function, faded photographs, nostalgic science"
    "034" = "Rich jewel tones for quantum entanglement, emerald ruby sapphire amethyst, luxurious"
    "035" = "Quantum art in oxidized copper and iron tones, patina textures, aged industrial"
    "036" = "Double slit experiment visualization, particle beam splitting, interference pattern forming on screen"
    "037" = "Abstract representation of Schrodinger cat, overlapping alive and dead states, quantum box"
    "038" = "Electron spin states visualization, up and down arrows, magnetic field lines, quantum spinning tops"
    "039" = "Quantum tunneling through energy barrier, particle ghosting through wall, probability cloud passing through"
    "040" = "Quantum decoherence process, organized patterns dissolving into chaos, environmental interference"
    "041" = "Single qubit in superposition, Bloch sphere representation, quantum computing foundation"
    "042" = "Bell test experiment setup, entangled photon pairs, measurement apparatus, correlation detection"
    "043" = "Heisenberg uncertainty principle, blurred position and momentum, complementary variables"
    "044" = "Wave function collapse moment, probability cloud crystallizing into definite state, observation effect"
    "045" = "Single photon visualization, light quantum, wave-packet traveling through space"
    "046" = "Quantum field fluctuations, virtual particles appearing and disappearing, vacuum energy"
    "047" = "Modern quantum atomic model, probability clouds replacing orbits, electron density distribution"
    "048" = "Matter-antimatter pair creation, particle-antiparticle emerging from energy, annihilation"
    "049" = "Planck scale visualization, spacetime foam, quantum gravity effects, smallest possible distances"
    "050" = "Many worlds interpretation, branching realities, parallel universe tree, quantum decisions"
    "051" = "Quantum foam texture, spacetime at smallest scales, bubbling reality, chaotic structure"
    "052" = "Entanglement swapping process, four particles, teleportation of quantum state"
    "053" = "Superposition gradually decaying, multiple states fading to one, time evolution"
    "054" = "Quantum eraser experiment, information destruction restoring interference, which-path detection"
    "055" = "EPR paradox visualization, Einstein Podolsky Rosen thought experiment, hidden variables challenge"
    "056" = "Perfectly centered symmetric quantum mandala, radial balance, meditative focus"
    "057" = "Quantum particle placed at rule of thirds intersection, dynamic tension, professional photography"
    "058" = "Diagonal composition of quantum wave propagation, dynamic movement, energy direction"
    "059" = "Frame within frame composition, quantum world seen through portal or microscope"
    "060" = "Mostly negative space with small quantum element, vast emptiness, cosmic scale"
    "061" = "Full bleed edge-to-edge quantum energy, immersive, no boundaries, overwhelming"
    "062" = "Triptych format showing quantum process in three stages, before during after"
    "063" = "Golden spiral composition with quantum elements following fibonacci curve, natural harmony"
    "064" = "Intentionally asymmetric unbalanced quantum composition, dynamic tension, modern art"
    "065" = "Multiple layers of depth with quantum elements at different z-positions, parallax effect"
    "066" = "Circular composition with quantum activity contained within sphere, contained universe"
    "067" = "Vertical flowing composition, quantum energy cascading downward, gravity-like"
    "068" = "Horizontal band composition, quantum timeline or spectrum, left to right reading"
    "069" = "Main quantum element anchored in corner, radiating outward, explosive origin"
    "070" = "Scattered composition with quantum particles randomly distributed, chaos theory"
    "071" = "Mysterious foggy quantum realm, half-hidden elements, enigmatic shadows, questions unanswered"
    "072" = "High-energy explosive quantum burst, dynamic motion blur, intense activity, powerful"
    "073" = "Serene calm quantum harmony, gentle waves, peaceful coexistence, meditation"
    "074" = "Chaotic quantum fluctuations, disorder entropy, unpredictable patterns, turbulent"
    "075" = "Melancholic fading quantum state, endings and decay, beautiful sadness, transience"
    "076" = "Triumphant quantum breakthrough, light emerging, discovery moment, eureka feeling"
    "077" = "Ominous dark quantum void, threatening unknown, cosmic horror, existential dread"
    "078" = "Playful whimsical quantum particles, bouncing energy, cartoon-like joy, fun science"
    "079" = "Romantic entanglement of particles, dance-like connection, love metaphor, intimate"
    "080" = "Clinical sterile quantum laboratory view, cold precision, scientific detachment"
    "081" = "Nostalgic retro-science quantum imagery, vintage equipment, old textbook illustrations"
    "082" = "Ultra-futuristic quantum technology, advanced civilization, transcendent science"
    "083" = "Spiritual transcendent quantum visualization, consciousness connection, enlightenment"
    "084" = "Industrial quantum machinery, factory of particles, mechanical production"
    "085" = "Organic living quantum system, biological metaphors, cells and growth, alive"
    "086" = "Cyberpunk noir quantum detective scene, rain neon shadows, mystery investigation"
    "087" = "Art deco meets space age, 1920s geometry in cosmic setting, retro-futurism"
    "088" = "Japanese ink painting with neon accents, tradition meets technology, east west fusion"
    "089" = "Steampunk Victorian quantum apparatus, brass gears copper pipes, anachronistic science"
    "090" = "Biotechnology organic quantum computer, living circuits, DNA computing, hybrid life"
    "091" = "Quantum realm as crystal cave interior, geometric mineral formations, natural precision"
    "092" = "Deep ocean quantum metaphor, bioluminescence, pressure darkness, alien beauty"
    "093" = "Cosmic spiritual mandala of quantum reality, sacred geometry, universal consciousness"
    "094" = "Electronic circuits growing like plants, quantum nature hybrid, technology evolution"
    "095" = "Ancient Egyptian hieroglyphics depicting quantum concepts, timeless knowledge"
    "096" = "Quantum mechanics as musical visualization, sound waves, rhythm patterns, synesthesia"
    "097" = "Quantum concepts as impossible architecture, Escher-like structures, spatial paradox"
    "098" = "Quantum uncertainty as weather system, probability clouds literally, atmospheric science"
    "099" = "Electron microscope aesthetic, quantum scale reality, scientific instrument view"
    "100" = "Dream-like surrealist quantum world, Dali-inspired melting reality, subconscious physics"
}

$total = $prompts.Count
$current = 0

foreach ($id in $prompts.Keys | Sort-Object) {
    $current++
    $prompt = $prompts[$id]
    $timestamp = Get-Date -Format "HH:mm:ss"
    Write-Host "`n[$timestamp] === Generating $id ($current/$total) ===" -ForegroundColor Cyan
    Write-Host "Prompt: $prompt" -ForegroundColor Gray
    
    $result = go run ./cmd/comfyui-test -prompt $prompt -width 1920 -height 1080 -steps 15 -output "images/experiments/$id.png" -timeout 20m 2>&1
    
    if ($LASTEXITCODE -eq 0) {
        Write-Host "SUCCESS: $id.png generated" -ForegroundColor Green
    } else {
        Write-Host "FAILED: $id - $result" -ForegroundColor Red
    }
}

Write-Host "`n`n=== ALL DONE ===" -ForegroundColor Yellow
Write-Host "Generated $total images in images/experiments/" -ForegroundColor Yellow



