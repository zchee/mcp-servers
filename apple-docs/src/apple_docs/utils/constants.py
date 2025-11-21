class ApiLimits:
    """Limits for API search results and related items."""

    MAX_SEARCH_RESULTS = 50
    MAX_RELATED_APIS = 10
    MAX_REFERENCES = 50
    MAX_SIMILAR_APIS = 15
    MAX_FRAMEWORK_DEPTH = 10
    DEFAULT_FRAMEWORK_DEPTH = 3

    # Default values for various operations
    DEFAULT_FRAMEWORK_SYMBOLS_LIMIT = 50
    DEFAULT_DOCUMENTATION_UPDATES_LIMIT = 50
    DEFAULT_TECHNOLOGY_OVERVIEWS_LIMIT = 50
    DEFAULT_SAMPLE_CODE_LIMIT = 50
    DEFAULT_TECHNOLOGIES_LIMIT = 200
    DEFAULT_REFERENCES_LIMIT = 20

    # Maximum values for schema validation
    MAX_FRAMEWORK_SYMBOLS_LIMIT = 200
    MAX_DOCUMENTATION_UPDATES_LIMIT = 200
    MAX_TECHNOLOGY_OVERVIEWS_LIMIT = 200
    MAX_SAMPLE_CODE_LIMIT = 200
    MAX_TECHNOLOGIES_LIMIT = 500
    MAX_REFERENCES_LIMIT = 50


class ProcessingLimits:
    """Limits for processing documentation content."""

    MAX_COLLECTIONS_TO_SHOW = 5
    MAX_RELATED_APIS_PER_SECTION = 3
    MAX_DOC_FETCHER_RELATED_APIS = 10
    MAX_DOC_FETCHER_REFERENCES = 15
    MAX_DOC_FETCHER_SIMILAR_APIS = 8
    MAX_DOC_FETCHER_REFS_PER_TYPE = 5

    RESPONSE_TIME_GOOD_THRESHOLD = 1000
    RESPONSE_TIME_MODERATE_THRESHOLD = 3000


class AppleUrls:
    """URLs for Apple Developer services."""

    BASE = "https://developer.apple.com"
    SEARCH = "https://developer.apple.com/search/"
    DOCUMENTATION = "https://developer.apple.com/documentation/"
    TUTORIALS_DATA = "https://developer.apple.com/tutorials/data/"
    TECHNOLOGIES_JSON = "https://developer.apple.com/tutorials/data/documentation/technologies.json"
    UPDATES_JSON = "https://developer.apple.com/tutorials/data/documentation/Updates.json"
    UPDATES_INDEX_JSON = "https://developer.apple.com/tutorials/data/index/updates"
    TECHNOLOGY_OVERVIEWS_JSON = "https://developer.apple.com/tutorials/data/documentation/TechnologyOverviews.json"
    TECHNOLOGY_OVERVIEWS_INDEX_JSON = "https://developer.apple.com/tutorials/data/index/technologyoverviews"
    SAMPLE_CODE_JSON = "https://developer.apple.com/tutorials/data/documentation/SampleCode.json"
    SAMPLE_CODE_INDEX_JSON = "https://developer.apple.com/tutorials/data/index/samplecode"


class RequestConfig:
    """Configuration for HTTP requests."""

    TIMEOUT = 30.0
    RETRIES = 3
    RETRY_DELAY = 1.0
    MAX_CONCURRENT_REQUESTS = 5
    USER_AGENT = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
    DEFAULT_SAFARI_USER_AGENT = "Mozilla/5.0 (Macintosh; arm64 Mac OS X 15_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15"


class ErrorMessages:
    """Standard error messages."""

    INVALID_URL = "URL must be from developer.apple.com"
    FETCH_FAILED = "Failed to fetch data from Apple Developer Documentation"
    PARSE_FAILED = "Failed to parse response data"
    NOT_FOUND = "Documentation not found (404). This URL may have been moved or removed."
    TIMEOUT = "Request timed out. Please try again later."
    NETWORK_ERROR = "Network error occurred. Please check your connection."
    RATE_LIMITED = "Request rate limit exceeded. Please wait before trying again."
    API_ERROR = "API error occurred while processing the request."
    CACHE_ERROR = "Cache operation failed, but the request will continue."
    VALIDATION_ERROR = "Input validation failed. Please check your parameters."
    SERVICE_UNAVAILABLE = "Apple Developer Documentation service is temporarily unavailable."
