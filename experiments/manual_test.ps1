# Manual model testing script
# Runs one model at a time for evaluation

param(
    [Parameter(Mandatory=$true)]
    [string]$Model,
    [switch]$NoThink
)

$testContent = @"
The Apollo 11 mission was the American spaceflight that first landed humans on the Moon. Commander Neil Armstrong and lunar module pilot Buzz Aldrin landed the Apollo Lunar Module Eagle on July 20, 1969, at 20:17 UTC. Armstrong became the first person to step onto the lunar surface six hours and 39 minutes later, on July 21 at 02:56 UTC. Aldrin joined him 19 minutes later. They spent about two and a quarter hours together exploring the site they had named Tranquility Base. Armstrong and Aldrin collected 47.5 pounds (21.5 kg) of lunar material to bring back to Earth. Command module pilot Michael Collins flew the Command Module Columbia alone in lunar orbit while they were on the Moon's surface.

Apollo 11 was launched by a Saturn V rocket from Kennedy Space Center on Merritt Island, Florida, on July 16 at 13:32 UTC, and it was the fifth crewed mission of NASA's Apollo program. The Apollo spacecraft had three parts: a command module (CM) with a cabin for the three astronauts; a service module (SM) that supported the command module with propulsion, electrical power, oxygen, and water; and a lunar module (LM) for the two astronauts who landed on the Moon.

The mission achieved its objective on July 20, 1969, when Armstrong and Aldrin landed on the Moon. Armstrong's first step onto the lunar surface was broadcast on live TV to a worldwide audience. He described the event as "one small step for [a] man, one giant leap for mankind." Apollo 11 ended on July 24 with a successful splashdown in the Pacific Ocean.

Key facts:
- Launch date: July 16, 1969
- Moon landing: July 20, 1969 at 20:17 UTC  
- First moonwalk: July 21, 1969 at 02:56 UTC
- Crew: Neil Armstrong (Commander), Buzz Aldrin (LM Pilot), Michael Collins (CM Pilot)
- Lunar samples: 47.5 pounds (21.5 kg)
- Landing site: Sea of Tranquility (Tranquility Base)
- Mission duration: 8 days, 3 hours, 18 minutes
- Splashdown: July 24, 1969 in Pacific Ocean
"@

$systemPrompt = @"
You extract informative content from web pages for encyclopedia articles.

Your task is to EXTRACT (not summarize) all useful factual content from the source.

OUTPUT FORMAT:
Use the same section structure as the source document. For each section:
# Section Heading
- Specific fact with concrete detail
- Another fact (include any names, dates, numbers, places)

EXTRACTION RULES:
- Include ALL facts: names, dates, numbers, statistics, places, events
- Preserve specific details - do not generalize or round numbers
- Each bullet should be a complete, informative statement

OUTPUT ONLY the extracted content without meta-commentary.
"@

$userPrompt = @"
Extract all factual content about "Apollo 11" from this source:

$testContent
"@

Write-Host "============================================" -ForegroundColor Cyan
Write-Host "Testing Model: $Model" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan
Write-Host ""

$think = if ($NoThink) { $false } else { $true }

$body = @{
    model = $Model
    messages = @(
        @{ role = "system"; content = $systemPrompt }
        @{ role = "user"; content = $userPrompt }
    )
    stream = $false
    think = $think
    options = @{
        temperature = 0.3
        num_predict = 4000
    }
} | ConvertTo-Json -Depth 10

Write-Host "Starting inference (think=$think)..." -ForegroundColor Yellow
$startTime = Get-Date

try {
    $response = Invoke-RestMethod -Uri "http://localhost:11434/api/chat" -Method Post -Body $body -ContentType "application/json" -TimeoutSec 600
    $endTime = Get-Date
    $duration = $endTime - $startTime
    
    $content = $response.message.content
    $thinking = $response.message.thinking
    $wordCount = ($content -split '\s+').Count
    
    Write-Host ""
    Write-Host "============================================" -ForegroundColor Green
    Write-Host "RESULTS for $Model" -ForegroundColor Green
    Write-Host "============================================" -ForegroundColor Green
    Write-Host "Duration: $($duration.TotalSeconds.ToString('F1'))s" -ForegroundColor White
    Write-Host "Word Count: $wordCount" -ForegroundColor White
    if ($thinking) {
        Write-Host "Thinking: $($thinking.Length) chars" -ForegroundColor White
    }
    Write-Host ""
    Write-Host "--- OUTPUT ---" -ForegroundColor Yellow
    Write-Host $content
    Write-Host ""
    Write-Host "--- END OUTPUT ---" -ForegroundColor Yellow
    
    # Save output
    $outputPath = "experiments/benchmark_results/manual_${Model -replace ':', '_'}.md"
    $content | Out-File -FilePath $outputPath -Encoding utf8
    Write-Host "Saved to: $outputPath" -ForegroundColor Gray
    
} catch {
    Write-Host "ERROR: $_" -ForegroundColor Red
}


