# 2026 Summer Internship Project

## The Goal

The goal of this project is *not* to have a wildly successful product. (I mean, that would be great, but that’s not what we’re shooting for here.) The goal of the project is to try something new and ***learn something***. The system we’re building is experimental; a proof-of-concept to see how computational statistics can be applied to the detection of problems with production systems.

## Background

This project and design proposal requires an understanding of a new core concepts:

### Observability & Other Messy Terms

You’ll hear the blanket term “observability” thrown around the industry. It was popularized by [Charity Majors](https://charity.wtf/) as a way for advocating for improvements in production tooling. The new term resonated, and is now the way people think about introspection into production systems.

The term “Observability” is commonly defined as the combination of collecting information (called *telemetry*) from several sources (the main ones being metrics, logs, and traces) and annotating this information with metadata that provides additional *context*. This context helps correlate information from multiple sources, and provides the information needed to drill down into specifics.

A big problem is that when people talk about observability, they tend to focus more on tooling and not about what problems they’re trying to solve with those tools. This happens because the same tools and similar processes are used to address very different use cases. Some notable examples:

* Monitoring production systems.
* Testing systems, either in production or in development.
* Measuring performance.
* Measuring compliance. (SOC2, HIPAA, GDPR, …)
* Security detection and response.
* Auditing access to systems.
* …

### The Specific Problem We’re Solving

Out of all those possible use cases, the problem we’re focusing on this summer is **monitoring production systems**, specifically services that respond to queries or other request-response style network traffic. The goal is to build a proof-of-concept of a system that does a better job on alerting operators about anomalous behavior.

The system we end up building might also be useful for other use cases, but that is not the focus of this summer project.

### What to Monitor

There are a couple of well-known ways of measuring system health in the industry. This project will focus on using the “[Four Golden Signals](https://sre.google/sre-book/monitoring-distributed-systems/#xref_monitoring_golden-signals)” from Google SRE, but there are other approaches out there. A notable one is Brendan Gregg’s [USE Method](https://www.brendangregg.com/usemethod.html), which focuses on machine-level performance.

#### The Four Golden Signals

This was a monitoring approach for web sites developed by Google SRE, documented in the book *Site Reliability Engineering*, by Beyer, et. al.
These signals will form the basis of this project. They are:

* **Latency** \- The time it takes to service a request.
* **Traffic** \- The number of requests per second.
* **Errors** \- The rate at which requests are failing.
* **Saturation** \- How “full” the service is. Common measures are: queue length, buffer usage, or concurrent requests in flight.

This is also sometimes referred to as “The RED Method”, which uses a slightly different phrasing:

* **Rate** \- Number of requests per second
* **Errors** \- The number of requests that are failing.
* **Duration** \- The amount of time those requests take.

They’re really the same thing. The Golden Signals \= RED \+ Saturation.
These approaches, with examples for how to implement them with Prometheus, are described in Tom Wilke’s excellent talk “The RED Method”. ([Video](https://www.youtube.com/watch?v=zk77VS98Em8),  [Slides](https://grafana.com/files/grafanacon_eu_2018/Tom_Wilkie_GrafanaCon_EU_2018.pdf?pg=the-red-method-how-to-instrument-your-services&plcmt=in-text))

Understanding these terms will give us a common language to use when talking to potential customers. Experienced system operators are familiar with these terms, and using them to describe the capabilities of our new thing will help them understand the value of it.

#### A Slight Generalization

For the purposes of this project, I would like to generalize these terms a bit. Here are my modified “four signals”:

* **Performance** \- Indicators that measure system performance in a way that is meaningful to the operator. “Latency” (or “Duration”) is just one example of such an indicator.
* **Load** \- A generalized indicator of how busy a system is. “Traffic” (or “Rate”) are specific examples of this, but “Utilization” from the USE Method could also be used for this.
* **Errors** \- Same
* **Utilization** \- In resource-constrained environments like bare-metal servers or containers, utilization indicators measure the percentage of constrained resources used. This is the same as the measure in the USE method.

Utilization replaces Saturation in this scheme. Really what you want is something that measures the [Carrying Capacity](https://en.wikipedia.org/wiki/Carrying_capacity) of the system, but that is difficult to know in advance. Resource utilization tends to be the best substitute.

#### KPIs

“[KPI](https://www.kpi.org/kpi-basics/)” stands for **K**ey **P**erformance **I**ndicator, another term that you’ll see quite often when talking about effective monitoring. It is often used to describe measurements that are less technical and more business-focused. I note this here because it might be possible to use KPIs along with other performance indicators.

### Alerting Criteria

Since this project is primarily about alerting on problems, it is worth spending some time reviewing the different approaches commonly used to determine when an alert should be fired.

#### Thresholds

The most basic technique, and the most commonly used, is to define a numeric threshold for a given metric. For example: alert me when CPU utilization is over 80% for more than 5 minutes. In this case, `0.8` is the threshold, and the alerting rule will fire when the utilization metric crosses that `0.8` threshold and stays above it for 5 minutes. The time parameter is there to prevent spurious alerts caused by temporary spikes in usage or even monitoring artifacts.

This approach has several pros and cons. The biggest benefit of this approach is that it is easy to understand, and easy to reason about its behavior. You can be woken up at 2am, look at this rule, and know exactly why it fired. 9 out of 10 times, the solution that wins in a team setting is the one that is easiest for all team members to understand. It’s also easy to change. You can look at the system’s behavior and easily recognize when the threshold needs to be bumped up

The biggest drawback of this approach is that it is spammy. For almost all measurements there is expected variation in the observed samples. Some queries take longer to process. Sometimes the request is being handled at the same time as some other process running on the same computer, and that slows it down. This happens all the time. The variation is *expected*, and you don’t want to be woken up at 2am for behavior that is considered “business as usual.” In practice teams are always fiddling the thresholds, “tuning” the alerts, trying to maximize effectiveness while minimizing [false positives](#measuring-alert-quality).

#### Service Level Objectives (SLO)

This is a more sophisticated approach also championed by Google SRE. In this method, you define a set of Service Level Indicators (SLIs) that should all be “true” in order to consider the service “healthy”. The “objective” part is a goal for what percentage of time a service is expected to be healthy. For example, a “4 nines” SLO means that the service should be considered healthy, as measured by the indicators, 99.99% of the time. See this description of “[Nines](https://en.wikipedia.org/wiki/High_availability#Percentage_calculation)”. There is a lot to say about this topic, if interested the defining book on the subject is *Implementing Service Level Objectives*, by Alex Hidalgo.

As far as alerting is concerned, the innovation here was introducing the concept of an *Error Budget*. In this, you start with a total measurement period. (Like a month.) You calculate how much of that time is allowed to be unhealthy. For a month, a 4-nines SLO means the service can only be unhealthy for 4.4 minutes. This is your error budget. The month is then broken down into smaller evaluation periods, like 30 seconds. The error budget can then be measured in “bad” evaluation intervals. In this example, the error budget is 8 bad intervals per month. You then track the burn rate of this error budget and alert when

This is a more robust approach in theory, but in practice it has several big problems. There are a bunch of social issues, as you can see by how much of the SLO book is devoted to selling this approach to non-technical people, and convincing them that the goal can’t be 100% availability. The more relevant problems here are:

* The higher the SLO, the smaller the evaluation interval needs to be for this to be useful. In practice this means you need to collect metrics with a high frequency, often 5 seconds or less. Nobody wants to do this, because it puts additional strain on production systems and makes monitoring more expensive. ([This can sometimes break the business\!](https://blog.pragmaticengineer.com/datadog-65m-year-customer-mystery/))
* It can sometimes wait too long to alert. Sometimes the system can be in a clearly bad state, but the system will wait until the error budget drops enough to trigger the alert.

#### Expected Distributions

This project hopes to use a more probabilistic approach. The idea is to create a statistical model of how the system is expected to behave, and then alert when the sampled system behavior is out-of-distribution. This is also threshold-based, but the threshold is based on a goodness-of-fit test and not individual samples. The big challenge of this approach is knowing when the distribution is expected to change. That is discussed in detail later in the document.

### Why Monitor?

A useful mental model of production monitoring is that it has three primary motivations:

#### <span style="color: red">**\[\!\]**</span> Detecting when something is going wrong.

This is used to detect situations when corrective action needs to be taken.
Used to notify an operator, either human or automated response systems.
Alerts should be specific, timely and actionable.

#### <span style="color: yellow">**\[?\]**</span> Trying to understand what happened.

Once something of interest has happened, the telemetry can be used to figure out *why* it happened.
This often pushes companies to collect all the information they can, because you don’t know in advance what they will need.
This includes debugging production issues, but also measuring and understanding longer-term trends.

#### <span style="color: green">**\[$\]**</span> Demonstrating value.

Measuring the system as a way of proving to others that the system is functioning, or that the people operating the system are doing their job.

How good are the different alerting approaches at accomplishing these goals?

|  | <span style="color: red">\[\!\]</span> | <span style="color: yellow">\[?\]</span> | <span style="color: green">\[$\]</span> |
| :---- | ----- | ----- | ----- |
| **Thresholds** | 👍<br>It gets the job done, but spammy alerts due to random variation that is expected.  | 👍👍<br>It tells you right away that a metric you care about is not good, helps focus the investigation on something measurable.  | 👎<br>Metrics and their thresholds are usually too low-level and technical for the business to care about. |
| **SLOs** | 👍👍<br> It does a better job of alerting you when something truly matters; very few false positives. Sometimes the alerts are not fired in a timely manner. | 👎<br> SLO percentages tell you nothing about what is going wrong. Best practice is to have a different set of dashboards and telemetry to aid in debugging. | 👍👍👍<br>SLOs are defined in ways that are meaningful to the business, and it is easy to track improvements and regressions over time. |
| **Distributions** | 👍👍👍<br>It tells you right away when something meaningful looks off. It handles random variation in a natural way.  | 👍👍<br>Like threshold alerting, this focuses the investigation initially on metrics that matter.  | 👍👍<br>Because the system tracks distributions, it is also able to tell you how many “nines” you’re getting from an SLO by looking at the tail of the distribution. It’s not as good at measuring this over an evaluation period. |

### Measuring Alert Quality

As I said at the beginning, this is an experiment. We need a way of measuring how our approach compares to the alternatives. There are some standard ways to do this, and a language to use when talking about it. It’s good to understand the different terminologies used when discussing the accuracy of alerts.

The first concept is the “True/False” \+ “Positive/Negative” categories. “True/False” indicates whether the alerting mechanism got the “right answer”, and “Positive” means an alert fired. This can be best understood by looking at the diagram below:

The best we can, we should count the number occurrences of each of these categories. Once we have that data, we can calculate a number of different measures, often used when talking about pattern recognition:

**Precision** 	\= (\# true positives) / (\# of positives) 	\= TP / (TP \+ FP)
**Recall** 		\= (\# true positives) / (\# of problems) 	\= TP / (TP \+ FN)
**Prevalence** 	\= (\# of problems) / (\# all) 		\= (TP \+ FN) / (TP \+ FP \+ TN \+ FN)
**Accuracy** 	\= (\# true ) / (\# all) 			\= (TP \+ TN) / (TP \+ FP \+ TN \+ FN)

These terms are discussed in more detail in the Wikipedia article [Precision and Recall](https://en.wikipedia.org/wiki/Precision_and_recall).

# Proposed Approach

At a high level, what we’re going to do is build a statistical model of service performance, and then alert human operators when the sampled telemetry is determined to be “too far out-of-distribution”. That verdict will be calculated using a goodness-of-fit test and an alerting threshold on the likelihood that the collected sample was drawn from the expected distribution.

The details are something we need to work through together over the course of the summer.

### Data Model

A “data model” is a term that describes the set of information about something interesting, and the way that set of information is organized.

For this project, I suggest starting with the concept of a “Service”. This will be a computer system, or component of that system, that should be monitored and alerted on separately. Some minimal information we need to collect about a service would be:

* The name of the service.
* The set of people that care about the service.
* For each person:
  * Display Name
  * Username / login
  * How they would like to be notified. (Email, Pager)
* The set of indicators that are meaningful for the service.
  * Performance indicators
  * Load indicators
  * Error indicators
  * Utilization indicators
* For each indicator:
  * The name of the indicator.
  * A [Prometheus](https://prometheus.io/) or [Loki](https://grafana.com/oss/loki/) query to sample it.
  * How often samples should be collected.
  * What labels should be used for drill-down.
* “Good” vs. “Bad” time periods for the service under observation. Collected through a combination of detection and user feedback.


We should start with one Performance and one Load indicator, and work that through a proof-of-concept before adding complexity.

### Detection Process

Once we have the queries for the indicators of interest, we should start collecting regular samples from them. The samples will be collected by running a query against an underlying observability system. For this summer, this will focus on querying data from [Prometheus](https://prometheus.io/) and/or [Loki](https://grafana.com/oss/loki/).

These raw samples will first be stored as-is, at least to start. Having the raw data may be useful for debugging, and may aid in compelling data visualizations. That said, these raw samples will need to be processed, and used for two purposes:

1. Running the goodness-of-fit tests to determine if the samples are in or out of distribution.
2. Updating the expected distribution.


The expected distribution will be in the form of an [Empirical Cumulative Distribution Function](https://en.wikipedia.org/wiki/Empirical_distribution_function) (ECDF). This is complicated enough, but that only looks at the indicator itself as an independent variable, and does not consider the factors that influence that indicator in reality. We’re going to do something more complex, which is to build a [Joint](https://en.wikipedia.org/wiki/Joint_probability_distribution) ECDF. For the first proof-of-concept, this will be “expected performance for a given load”. “P(Perf | Load)” in [Bayesian](https://en.wikipedia.org/wiki/Bayes%27_theorem) terms. The load indicator will be the independent variable, and the performance indicator will be considered a [dependent variable](https://en.wikipedia.org/wiki/Dependent_and_independent_variables).

To evaluate whether to fire an alert, take the last N samples (where N is either a sample count or a time window, TBD) and run a goodness-of-fit test to determine the probability that the samples were drawn from the latest ECDF, given the (average?) load over this time period. We can start with a [KS test](https://en.wikipedia.org/wiki/Kolmogorov%E2%80%93Smirnov_test) but it would be worth trying others and measuring the effectiveness of different approaches. If the calculated probability is less than some threshold, then fire an alert.

You’ll notice there are a lot of missing details here. What value of N should we use? What threshold? Testing different approaches and figuring out what works and what doesn’t is part of the work we will be doing this summer. Experimentation and measurement is required.

#### Being Out-of-Distribution is Expected, Sometimes

One of the challenges with this approach is that there are many times when a service’s behavior will be out-of-distribution under normal operating conditions.
Some notable examples:

* **Lifecycle Events** \- When the service is first starting up, shutting down, or restarting.
* **Updates** \- A new version may have different performance characteristics.
* **Changes in customer behavior** \- Maybe a user starts sending more “expensive” queries that use more resources than normal.
* **Events** \- Known events that will drive higher-than-normal load, including: launches, new releases, Black Friday & Cyber Monday (BFCM).
* **Seasonality** \- You get different load patterns based on the time of day, day of week, or even the day of year. Example: GrubHub is *much* busier around meal times.

A useful alerting solution will need to take these into account. Otherwise, all these “business as usual” situations will cause alert spam and make the solution less useful to human operators.

#### Lifecycle Detection

The simplest way of addressing this is to add mechanisms that attempt to automatically detect these “business as usual” conditions and suppress alerts for some time interval after the event.

The easiest to detect are lifecycle indicators. Some examples: Prometheus has a special “up” metric that tells you whether a service is running at all. In addition, there is usually an “uptime” metric that can tell you if service has recently restarted. If the service has a metric that exports the build version, a change to this metric is a signal of a new version rollout that might have a different performance profile.

#### Alerting on Load

The above will tell you when the performance of a system is unexpected for a given load, but what if unexpected load ***is*** the problem? This can happen due to traffic spikes, DDoS (Distributed Denial of Service) attacks, error+retry storms, etc. We should also create a model of the expected load for the service, and alert when the load itself is anomalous.

This will get into the seasonality effects discussed earlier. We can address this by using joint distributions, just like we did for performance. Load is the dependent variable, and *time* is the independent variable. We should track the following joint ECDFs:

* Load given time-of-day
* Load given day-of-week
* Load given day-of-month (???)
* Load given day-of-quarter (???)
* Load given day-of-year (???)

The problem with the last few is that it needs a lot of history to be useful. Most monitoring systems will retain at least 7 days of data, but you can’t always count on more than that. We should perhaps detect when we don’t have enough history and skip or disable these.

#### Known Events

This still doesn’t solve the “launch day” or BFCM problems. Ideally a team would be gathering performance data using load tests, and we’d have data coverage for the increased load we would see during these events. Sadly, that’s not the sort of thing you can count on… Most companies launch as fast as possible and “test in prod.”

For this project, we’re going to have to leave this as a product gap. It might be fine to instead recommend silencing alerts during these events.

### Measuring Accuracy

As mentioned above, we would like to track the number of false positives and related counts so we can measure alert quality. How do we know, though? Unfortunately, we can’t… Not without getting more information from the system operators that are triaging the alerts. That means we need to ask for a couple of pieces of important feedback:

1. **Was this a real problem?** \- This could be a 👍/👎 button in the web UI, a feedback link in the alert itself, or a post-incident survey. We should assume that it is a real problem by default, and depend on humans to complain if that is not true.
2. **“Good” vs. “Bad” Time Ranges** \- After an incident is observed, we may want to collect timestamps from the user of when the bad behavior started, and when it ended. This will be our signal about what data to include in the “expected distributions” and what data should be excluded. We may also need to ask ***why*** it is bad. Consider the case of running a load test. The time period could be considered “bad” (or out-of-distribution) for modeling seasonality effects, but “good” for modeling performance indicators.

#### What About the Control?

In any experiment where you’re trying to determine if something is better than an alternative, you need to measure both the experiment and a control system. We should discuss how to set something like this over the summer. I suspect a good approach would be to set up traditional threshold and SLO alerting for a test system, and then gather alert quality metrics for all. Then when presenting results at the end of the summer we can demonstrate it with data.

That also means we will need to track other metrics beyond just a count of false positives. For example: How many times did an operator need to adjust the alerting thresholds? How many times did the SLO get adjusted?

## Appendix

### Ideas for Future Work

There are many ideas that we will likely not have time to work on this summer. If we do happen to make rapid progress, we should consider trying some of these out to make the product better:

* Figure out how to build a joint Poisson distribution (useful for modelling time between event occurrence) of error rates given load.
* Track other forms of seasonality and use that to improve the statistical models of load. That will require something more sophisticated than the joint ECDFs we will be using. It will likely require the use of correlation cubes from the full Uncertain Tea product.
* Making it work with multiple indicators per category. (Use a cross-product of different indicators when building joint distributions.)
* Detecting auto-correlation. This could be helpful when individual samples are not independent.
* Would adding in saturation indicators be useful?
* Calendars of notable events that might need silences.
