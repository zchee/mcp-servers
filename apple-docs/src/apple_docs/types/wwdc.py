from typing import TypedDict


class TranscriptSegment(TypedDict):
    """A segment of a video transcript."""

    timestamp: str
    text: str


class TranscriptData(TypedDict):
    """Complete transcript data for a video."""

    fullText: str
    segments: list[TranscriptSegment]


class CodeExample(TypedDict):
    """A code example extracted from a video."""

    timestamp: str | None
    title: str | None
    language: str
    code: str
    context: str | None


class ResourceLink(TypedDict):
    """A link to an external resource."""

    title: str
    url: str


class VideoResources(TypedDict):
    """Resources associated with a video."""

    hdVideo: str | None
    sdVideo: str | None
    sampleProject: str | None
    slides: str | None
    resourceLinks: list[ResourceLink] | None


class RelatedVideo(TypedDict):
    """A related video reference."""

    id: str
    year: str
    title: str
    url: str


class Chapter(TypedDict):
    """A video chapter."""

    title: str
    timestamp: str
    duration: str | None


class WWDCVideo(TypedDict):
    """Detailed information about a WWDC video."""

    id: str
    year: str
    url: str
    title: str
    speakers: list[str] | None
    duration: str
    topics: list[str]
    hasTranscript: bool
    hasCode: bool
    transcript: TranscriptData | None
    codeExamples: list[CodeExample] | None
    chapters: list[Chapter] | None
    resources: VideoResources
    relatedVideos: list[RelatedVideo] | None
    extractedAt: str | None


class YearMetadata(TypedDict):
    """Metadata for a specific WWDC year."""

    year: str
    id: str
    hasCodeTab: bool
    extractedAt: str
    lastUpdated: str | None


class WWDCYearData(TypedDict):
    """Complete data for a specific WWDC year."""

    metadata: YearMetadata
    videos: list[WWDCVideo]


class TopicInfo(TypedDict):
    """Information about a WWDC topic."""

    id: str
    name: str
    url: str


class Statistics(TypedDict):
    """Global statistics about WWDC content."""

    byTopic: dict[str, int]
    byYear: dict[str, int]
    videosWithCode: int
    videosWithTranscript: int
    videosWithResources: int


class GlobalMetadata(TypedDict):
    """Global metadata for the WWDC archive."""

    version: str
    lastUpdated: str
    topics: list[TopicInfo]
    years: list[str]
    statistics: Statistics


class IndexVideoItem(TypedDict):
    """Summary information for a video in an index."""

    id: str
    year: str
    title: str
    topics: list[str]
    duration: str
    hasCode: bool
    hasTranscript: bool
    dataFile: str


class TopicIndex(TypedDict):
    """Index of videos for a specific topic."""

    id: str
    name: str
    years: list[str]
    videos: list[IndexVideoItem]


class YearIndex(TypedDict):
    """Index of videos for a specific year."""

    year: str
    topics: list[str]
    videos: list[IndexVideoItem]
